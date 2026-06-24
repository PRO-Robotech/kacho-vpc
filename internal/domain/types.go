// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package domain — newtypes общего назначения для VPC-ресурсов и их Validate().
//
// Семантически-нагруженные поля (Name/Description/Labels) — не голый `string` /
// `map[string]string`, а self-validating newtypes: вся валидация живет в домене,
// он становится источником истины. Все newtypes реализуют единый контракт
// `Validate() error`, возвращая доменную `*ValidationError` (stdlib, без gRPC);
// трансляция в gRPC InvalidArgument — serviceerr.FromValidation.
package domain

import (
	"regexp"
	"unicode/utf8"
)

// ---- Newtypes для базовых строковых полей -----------------------------------

// RcNameVPC — разрешительное имя для VPC-ресурсов (Network, Subnet, Address,
// RouteTable, SecurityGroup, NetworkInterface). Допускает пустую строку,
// uppercase и underscore; длина 0..63.
type RcNameVPC string

// RcDescription — описание ресурса; UTF-8 длина ≤ 256.
type RcDescription string

// ---- Labels (typed map key/value) -------------------------------------------

// LabelKey — ключ label (`^[a-z][-_./\\@a-z0-9]{0,62}$`, 1..63 bytes).
type LabelKey string

// LabelVal — значение label (0..63 bytes).
type LabelVal string

// RcLabels — набор labels с типизированными key/value. Тонкая обертка над map,
// чтобы domain не зависел от сторонних контейнеров; контракт — Len/Get/Put/Iterate.
type RcLabels map[LabelKey]LabelVal

// Len возвращает число пар.
func (d RcLabels) Len() int { return len(d) }

// Get возвращает значение по ключу и признак наличия.
func (d RcLabels) Get(k LabelKey) (LabelVal, bool) {
	v, ok := d[k]
	return v, ok
}

// Put кладет пару, лениво инициализируя набор (zero value пригоден к использованию).
func (d *RcLabels) Put(k LabelKey, v LabelVal) {
	if *d == nil {
		*d = make(RcLabels)
	}
	(*d)[k] = v
}

// Iterate обходит пары; возврат false из fn останавливает обход. Порядок не определен.
func (d RcLabels) Iterate(fn func(LabelKey, LabelVal) bool) {
	for k, v := range d {
		if !fn(k, v) {
			return
		}
	}
}

// ---- Regex'ы (синхронизированы с corelib/validate; источник истины здесь) ---

var (
	nameVPCRe  = regexp.MustCompile(`^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$`)
	labelKeyRe = regexp.MustCompile(`^[a-z][-_./\\@a-z0-9]{0,62}$`)
)

const (
	// MaxNameLen — максимум для Name полей ресурсов.
	MaxNameLen = 63
	// MaxDescriptionLen — лимит описания (UTF-8 rune count).
	MaxDescriptionLen = 256
	// MaxLabels — максимальное число label-пар на ресурс.
	MaxLabels = 64
	// MaxLabelKeyLen — длина ключа label в байтах.
	MaxLabelKeyLen = 63
	// MaxLabelValueLen — длина значения label в байтах.
	MaxLabelValueLen = 63
)

// ---- Validate()-методы ------------------------------------------------------

// Validate проверяет, что value соответствует разрешительному name-контракту
// для VPC-ресурсов. Пустая строка / uppercase / underscore — OK.
// Длина 0..63 (regex это уже включает).
func (n RcNameVPC) Validate() error {
	if !nameVPCRe.MatchString(string(n)) {
		return newValidationError("name", `name must match ^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$ (letters, digits, hyphens, underscores; starts with letter; up to 63 chars; empty allowed)`)
	}
	return nil
}

// Validate проверяет длину description (UTF-8 rune count ≤ MaxDescriptionLen).
func (d RcDescription) Validate() error {
	if utf8.RuneCountInString(string(d)) > MaxDescriptionLen {
		return newValidationError("description", "description length exceeds 256 chars")
	}
	return nil
}

// Validate проверяет LabelKey-регекс (1..63 bytes, lowercase letters / digits /
// `-_./\\@`).
func (k LabelKey) Validate() error {
	s := string(k)
	if len(s) == 0 || len(s) > MaxLabelKeyLen || !labelKeyRe.MatchString(s) {
		return newValidationError("labels."+s, "invalid label key (1..63 chars, lowercase letters, digits, _-./\\@)")
	}
	return nil
}

// Validate проверяет LabelVal (0..63 bytes; пустая строка OK).
func (v LabelVal) Validate() error {
	if len(string(v)) > MaxLabelValueLen {
		return newValidationError("labels", "label value exceeds 63 chars")
	}
	return nil
}

// ValidateLabels пробегает по всем парам RcLabels и валидирует ключ + значение.
// Аналог corevalidate.Labels: возвращает первую ошибку (как и старый код), плюс
// дополнительно проверяет cardinality ≤ MaxLabels.
//
// Это свободная функция, а не метод: набор label'ов валидируется в контексте
// всего ресурса, поэтому вызов `ValidateLabels(n.Labels)` из `Network.Validate()`
// читается естественнее отдельного `Labels.Validate()`.
func ValidateLabels(labels RcLabels) error {
	if labels.Len() > MaxLabels {
		return newValidationError("labels", "too many labels (max 64)")
	}
	var firstErr error
	labels.Iterate(func(k LabelKey, v LabelVal) bool {
		if err := k.Validate(); err != nil {
			firstErr = err
			return false
		}
		if err := v.Validate(); err != nil {
			firstErr = err
			return false
		}
		return true
	})
	return firstErr
}

// ---- Helpers для конверсии RcLabels ↔ map[string]string ----------------------

// LabelsFromMap конвертирует обычный map[string]string в RcLabels.
// Используется в handler-слое: gRPC request приходит с map[string]string,
// внутри домена он становится RcLabels. nil-map → пустой RcLabels.
func LabelsFromMap(m map[string]string) RcLabels {
	var d RcLabels
	for k, v := range m {
		d.Put(LabelKey(k), LabelVal(v))
	}
	return d
}

// LabelsToMap — обратное преобразование, для DTO (dto/toproto).
// Возвращает nil если RcLabels пуст: пустой ресурс без labels отдает `Labels: nil`
// в proto (labels отсутствует в JSON).
func LabelsToMap(d RcLabels) map[string]string {
	if d.Len() == 0 {
		return nil
	}
	m := make(map[string]string, d.Len())
	d.Iterate(func(k LabelKey, v LabelVal) bool {
		m[string(k)] = string(v)
		return true
	})
	return m
}

// ---- Equal helpers ----------------------------------------------------------

// LabelsEqual — set-equality для RcLabels: равны, если одинаковое число пар
// и каждая пара (key→value) совпадает. Порядок (как у map) — не важен.
//
// Используется в `<Resource>.Equal()` для noop-detection в Update-flow и в
// equality-проверках use-case тестов. Порядок (как у map) не важен.
func LabelsEqual(a, b RcLabels) bool {
	if a.Len() != b.Len() {
		return false
	}
	equal := true
	a.Iterate(func(k LabelKey, v LabelVal) bool {
		bv, ok := b.Get(k)
		if !ok || bv != v {
			equal = false
			return false
		}
		return true
	})
	return equal
}

// stringSlicesEqual — order-sensitive equality для []string (reference-id
// массивов: SecurityGroupIDs, V4AddressIDs, V6AddressIDs у NIC). Для consistency
// выбран order-sensitive вариант: порядок reference-id фиксирован сервис-слоем
// (validate + insert) и не должен меняться без явного intent'а в Update.
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// labelsMapEqual — equality для `map[string]string` (rule-level labels у
// SecurityGroupRule, см. domain/security_group.go: rule.Labels не RcLabels —
// JSONB round-trip ограничение). Order-insensitive (map-семантика).
func labelsMapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
