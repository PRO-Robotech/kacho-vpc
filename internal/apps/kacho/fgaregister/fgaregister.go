// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package fgaregister описывает owner-hierarchy tuple через transactional-outbox.
//
// Созданному/удаленному VPC-ресурсу нужен owner-hierarchy tuple в FGA, чтобы
// per-resource Check (Get/Update/Delete) разрешался по каскаду `<rel> from
// project`. Прямая best-effort запись в FGA после коммита строки ресурса была
// небезопасна: transient-сбой FGA проглатывался, tuple терялся навсегда →
// per-resource Check = DENY → пользователь создал ресурс и не видит его.
//
// Здесь INTENT на tuple пишется outbox-строкой в той же writer-TX, что
// вставляет/удаляет ресурс (один commit, без dual-write). Отдельный
// register-drainer (corelib outbox/drainer) позже применяет каждый intent через
// kacho-iam InternalIAMService.RegisterResource/UnregisterResource по mTLS
// (idempotent, at-least-once, retry на Unavailable — tuple durable и не теряется).
// Writer-сторона — FGARegister-emitter в internal/repo/kacho; drainer-сторона —
// IAM register-applier в internal/clients.
//
// Пакет — чистый domain (только stdlib): без pgx/grpc/transport-зависимостей,
// он лишь задает форму tuple/intent, на которую опираются и repo-writer, и
// clients-applier (dependency rule из architecture.md).
package fgaregister

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Registrar — порт синхронной регистрации owner-tuple'ов в kacho-iam. Create-flow
// после успешного коммита ресурса синхронно регистрирует те же Item'ы, что
// эмитятся в outbox-intent, чтобы owner-grant был доступен сразу — без гонки с
// async register-drainer'ом. Реализация — adapter поверх
// InternalIAMService.RegisterResource (idempotent); drainer остается
// at-least-once backstop'ом. nil-registrar (dev/no-iam) → sync-путь
// пропускается, остается только async.
type Registrar interface {
	Register(ctx context.Context, items []Item) error
}

// Типы событий в колонке `event_type` таблицы fga_register_outbox; передаются
// drainer-Applier'у и определяют, что он вызовет: RegisterResource (записать
// tuple) или UnregisterResource (снять tuple). Стабильные строки — часть on-row
// контракта; дополнительно энфорсятся DB CHECK на колонке.
const (
	// EventRegister — применить tuple через InternalIAMService.RegisterResource
	// (Create ресурса). Idempotent: повторная регистрация того же tuple → OK.
	EventRegister = "fga.register"
	// EventUnregister — снять tuple через InternalIAMService.UnregisterResource
	// (Delete ресурса). Idempotent: unregister отсутствующего tuple → OK.
	EventUnregister = "fga.unregister"
)

// Tuple — один owner-hierarchy relationship tuple, сериализуемый как payload
// одной строки fga_register_outbox. Поля 1:1 соответствуют полям запроса
// InternalIAMService RegisterResource/UnregisterResource:
//
//	SubjectId  — напр. "project:proj-aaaaaaaaaaaaaaaaa"
//	Relation   — напр. "project"
//	Object     — напр. "vpc_network:net-xxxxxxxxxxxxxxxxx"
//
// Один tuple == одна outbox-строка: drainer claim'ит и применяет строки
// независимо, поэтому poison/transient tuple не блокирует остальные.
type Tuple struct {
	SubjectID string `json:"subject_id"`
	Relation  string `json:"relation"`
	Object    string `json:"object"`
}

// Valid сообщает, полностью ли заполнен tuple. Неполный tuple — баг на стороне
// вызывающего: drainer-декодер считает его permanent (poison) и не ретраит.
func (t Tuple) Valid() bool {
	return t.SubjectID != "" && t.Relation != "" && t.Object != ""
}

// Item — один owner-hierarchy tuple плюс mirror-feed (labels + parent_project_id)
// для этого ресурса. Один Item == одна строка fga_register_outbox == один Payload.
// Labels/ParentProjectID — per-tuple (Network.Create эмитит tuple сети с ее
// labels и отдельный tuple default-SG), а не общие на весь Intent.
//
// Labels/ParentProjectID опциональны: Unregister и Create без labels эмитят Item
// с пустым mirror-feed (только tuple), что совпадает с back-compat-формой
// «голой» tuple-строки.
type Item struct {
	Tuple           Tuple
	Labels          map[string]string
	ParentProjectID string
}

// Intent — набор owner-hierarchy Item'ов для register (или unregister) одного
// lifecycle-события ресурса. FGARegister-emitter в repo разворачивает Intent в
// одну outbox-строку (один Payload) на каждый Item внутри writer-TX ресурса.
//
// Большинство VPC-ресурсов дают один project-hierarchy tuple; Network.Create
// дополнительно дает inline-tuple default-SG, поэтому Intent несет slice, а не
// единственный Item.
type Intent struct {
	Items []Item
}

// Payload — JSONB-форма одной строки fga_register_outbox: tuple, развернутый на
// верхний уровень (back-compat с прежней «голой» Tuple-строкой), плюс mirror-feed
// (labels + parent_project_id + монотонный source_version). На эту форму
// опираются и repo-writer (emit), и clients-applier (forward в kacho-iam
// RegisterResource).
//
// Tuple встроен (embedded), поэтому legacy-строка {"subject_id","relation","object"}
// декодируется прямо в Payload.Tuple с пустыми Labels/ParentProjectID и нулевым
// SourceVersion (в IAM трактуется как -infinity, никогда не выигрывает монотонное
// сравнение).
type Payload struct {
	Tuple
	// Labels — копия labels владельца-ресурса (mirror для selector'а).
	Labels map[string]string `json:"labels,omitempty"`
	// ParentProjectID — id владеющего проекта (parent-scope / containment).
	ParentProjectID string `json:"parent_project_id,omitempty"`
	// SourceVersion — монотонный per-object маркер, штампуется от DB-часов
	// (now()) на момент INSERT внутри writer-TX. Ноль (legacy/без штампа) → IAM
	// трактует как -infinity.
	SourceVersion time.Time `json:"source_version,omitempty"`
}

// Valid сообщает, полностью ли заполнен вложенный tuple.
func (p Payload) Valid() bool { return p.Tuple.Valid() }

// Encode сериализует Payload в JSONB-payload строки fga_register_outbox.
func Encode(p Payload) ([]byte, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("encode fga register payload: %w", err)
	}
	return b, nil
}

// Decode разбирает payload одной строки fga_register_outbox в Payload.
// Legacy-строка с голым Tuple декодируется с пустыми Labels/ParentProjectID и
// нулевым SourceVersion.
func Decode(b []byte) (Payload, error) {
	var p Payload
	if err := json.Unmarshal(b, &p); err != nil {
		return Payload{}, fmt.Errorf("decode fga register payload: %w", err)
	}
	return p, nil
}

// ProjectHierarchy строит канонический tuple `project:<projectID> #project
// @<objectType>:<objectID>`, который нужен каждому VPC-ресурсу, чтобы
// per-resource Check разрешался по каскаду `<rel> from project`. objectType —
// FGA-тип vpc_* ("vpc_network", "vpc_subnet", ...).
func ProjectHierarchy(projectID, objectType, objectID string) Tuple {
	return Tuple{
		SubjectID: "project:" + projectID,
		Relation:  "project",
		Object:    objectType + ":" + objectID,
	}
}

// RegisterIntent — удобный конструктор register-Intent из голых tuple'ов (без
// mirror-feed). Каждый tuple становится Item'ом с пустыми Labels/ParentProjectID;
// используется Unregister'ом и tuple-only call site'ами.
func RegisterIntent(tuples ...Tuple) Intent {
	items := make([]Item, 0, len(tuples))
	for _, t := range tuples {
		items = append(items, Item{Tuple: t})
	}
	return Intent{Items: items}
}

// RegisterItems — конструктор с mirror-feed: каждый Item несет свой tuple плюс
// labels + parent_project_id владельца-ресурса.
func RegisterItems(items ...Item) Intent { return Intent{Items: items} }

// ProjectHierarchyItem строит Item: канонический project-hierarchy tuple плюс
// labels + parent_project_id (= projectID) владельца-ресурса для mirror-feed.
// labels nil → пустой mirror-feed (ресурс без labels).
func ProjectHierarchyItem(projectID, objectType, objectID string, labels map[string]string) Item {
	return Item{
		Tuple:           ProjectHierarchy(projectID, objectType, objectID),
		Labels:          labels,
		ParentProjectID: projectID,
	}
}
