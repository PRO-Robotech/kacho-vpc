// Package domain — newtypes общего назначения для VPC-ресурсов и их Validate().
//
// Это первая итерация Wave 2 evgeniy migration plan (см. workspace
// docs/specs/evgeniy-migration-plan.md, Phase 1: KAC-99/KAC-94). Цель — заменить
// голый `string` / `map[string]string` для семантически-нагруженных полей
// (Name/Description/Labels) на self-validating newtypes. Validate()-логика
// съезжает из service/-слоя (`corevalidate.NameVPC/Description/Labels`) в
// домен — domain становится источником истины. Все newtypes реализуют
// единый контракт `Validate() error` (gRPC InvalidArgument).
//
// Pilot применён к ресурсу Network (см. domain/network.go); остальные 7
// ресурсов будут мигрированы отдельными задачами (KAC-100..106).
package domain

import (
	"regexp"
	"unicode/utf8"

	"github.com/H-BF/corlib/pkg/dict"
	"github.com/H-BF/corlib/pkg/option"
	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
)

// ---- Newtypes для базовых строковых полей -----------------------------------

// RcNameVPC — verbatim YC permissive name для VPC-ресурсов (Network, Subnet,
// Address, RouteTable, SecurityGroup, PrivateEndpoint, NetworkInterface).
// Регекс эквивалентен YC `/|[a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?/` —
// допускает empty + uppercase + underscore; длина 0..63.
type RcNameVPC string

// RcNameStrict — strict name для resource-manager (Cloud/Folder) и других
// strict-policy ресурсов. Регекс эквивалентен YC `/[a-z]([-a-z0-9]{0,61}[a-z0-9])?/`,
// длина 2..63. В kacho-vpc сейчас не используется, определён здесь ради
// consistency (Wave 2 будет реплицирован в kacho-resource-manager).
type RcNameStrict string

// RcDescription — описание ресурса; UTF-8 длина ≤ 256.
type RcDescription string

// RcNameOpt — optional RcNameVPC. Используется в типах, где имя может быть
// "не задано клиентом" (по верхнеуровневому контракту YC — это empty string,
// но в domain мы умеем отделить «not set» от «set to empty» через option).
type RcNameOpt = option.ValueOf[RcNameVPC]

// ---- Labels (map с newtype key/value через H-BF/corlib/dict) ----------------

// LabelKey — ключ label (`^[a-z][-_./\\@a-z0-9]{0,62}$`, 1..63 bytes).
type LabelKey string

// LabelVal — значение label (0..63 bytes).
type LabelVal string

// RcLabels — labels-набор: dict.HDict с typed key/value. Iterate/Get/Put —
// см. github.com/H-BF/corlib/pkg/dict.
type RcLabels = dict.HDict[LabelKey, LabelVal]

// ---- Regex'ы (синхронизированы с corelib/validate; источник истины здесь) ---

var (
	nameVPCRe    = regexp.MustCompile(`^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$`)
	nameStrictRe = regexp.MustCompile(`^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$`)
	labelKeyRe   = regexp.MustCompile(`^[a-z][-_./\\@a-z0-9]{0,62}$`)
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

// Validate проверяет, что value соответствует verbatim YC permissive name-
// контракту для VPC-ресурсов. Пустая строка / uppercase / underscore — OK.
// Длина 0..63 (regex это уже включает).
func (n RcNameVPC) Validate() error {
	if !nameVPCRe.MatchString(string(n)) {
		return coreerrors.InvalidArgument().
			AddFieldViolation("name", `name must match ^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$ (letters, digits, hyphens, underscores; starts with letter; up to 63 chars; empty allowed)`).
			Err()
	}
	return nil
}

// Validate проверяет strict-name контракт (resource-manager / Folder / Cloud).
func (n RcNameStrict) Validate() error {
	if !nameStrictRe.MatchString(string(n)) {
		return coreerrors.InvalidArgument().
			AddFieldViolation("name", `name must match ^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$ (lowercase letters, digits, hyphens; starts with letter; ends with letter or digit; 2..63 chars)`).
			Err()
	}
	return nil
}

// Validate проверяет длину description (UTF-8 rune count ≤ MaxDescriptionLen).
func (d RcDescription) Validate() error {
	if utf8.RuneCountInString(string(d)) > MaxDescriptionLen {
		return coreerrors.InvalidArgument().
			AddFieldViolation("description", "description length exceeds 256 chars").
			Err()
	}
	return nil
}

// Validate проверяет LabelKey-регекс (1..63 bytes, lowercase letters / digits /
// `-_./\\@`).
func (k LabelKey) Validate() error {
	s := string(k)
	if len(s) == 0 || len(s) > MaxLabelKeyLen || !labelKeyRe.MatchString(s) {
		return coreerrors.InvalidArgument().
			AddFieldViolation("labels."+s, "invalid label key (1..63 chars, lowercase letters, digits, _-./\\@)").
			Err()
	}
	return nil
}

// Validate проверяет LabelVal (0..63 bytes; пустая строка OK).
func (v LabelVal) Validate() error {
	if len(string(v)) > MaxLabelValueLen {
		return coreerrors.InvalidArgument().
			AddFieldViolation("labels", "label value exceeds 63 chars").
			Err()
	}
	return nil
}

// ValidateLabels пробегает по всем парам RcLabels и валидирует ключ + значение.
// Аналог corevalidate.Labels: возвращает первую ошибку (как и старый код), плюс
// дополнительно проверяет cardinality ≤ MaxLabels.
//
// Это свободная функция, а не метод RcLabels.Validate() — у `dict.HDict` нет
// receiver-метода Validate, оборачивать его в отдельный wrapping-тип ради
// одной кнопки слишком тяжело; вызов через ValidateLabels(n.Labels) в
// Network.Validate() семантически идентичен.
func ValidateLabels(labels RcLabels) error {
	if labels.Len() > MaxLabels {
		return coreerrors.InvalidArgument().
			AddFieldViolation("labels", "too many labels (max 64)").
			Err()
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

// LabelsToMap — обратное преобразование, для DTO (protoconv / dto/type2pb).
// Возвращает nil если RcLabels пуст — это match старому поведению
// `Labels: nil` в proto (verbatim YC: пустой ресурс без labels — labels отсутствует
// в JSON).
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
