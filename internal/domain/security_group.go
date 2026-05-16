package domain

import "go.uber.org/multierr"

// SecurityGroup — domain-сущность Security Group (Wave 2 batch B, KAC-94).
//
// Семантически-нагруженные поля (Name/Description/Labels) — newtypes из
// `domain/types.go` со встроенным Validate(). `CreatedAt` сюда НЕ входит —
// DB-managed, живёт в `SecurityGroupRecord` (см. `domain/persistence.go`)
// согласно skill evgeniy §4 D.1 / §7 H.1.
//
// `Status` — enum `SecurityGroupStatus` вместо голого string (skill §4 D.8 /
// AP-2). `Rules` хранятся embedded (JSONB в БД); каждое правило — `SecurityGroupRule`
// с собственными newtype-полями (Description/Labels).
//
// `ID` / `FolderID` / `NetworkID` — голый `string` (внешние reference-id;
// валидация — на уровне `corevalidate.ResourceID` в service-слое).
type SecurityGroup struct {
	ID                string
	FolderID          string
	NetworkID         string
	Name              RcNameVPC
	Description       RcDescription
	Labels            RcLabels
	Status            SecurityGroupStatus
	DefaultForNetwork bool
	Rules             []SecurityGroupRule
}

// Validate проверяет name/description/labels по domain-контракту + статус
// (если выставлен — должен быть из набора SecurityGroupStatus*). Вызывается
// use-case-слоем ПЕРЕД repo.Insert / repo.Update (skill evgeniy §4 D.4 / D.6).
//
// Rules валидируются отдельно в service-слое (validateSGRule) — там CIDR-проверка
// host-bits, ports-диапазон и т.п. cross-cutting concerns, которые не выражаются
// одним newtype'ом.
func (s SecurityGroup) Validate() error {
	errs := []error{
		s.Name.Validate(),
		s.Description.Validate(),
		ValidateLabels(s.Labels),
	}
	for _, r := range s.Rules {
		errs = append(errs, r.Validate())
	}
	return multierr.Combine(errs...)
}

// Equal — deep equality по domain-полям. `CreatedAt` не входит (skill evgeniy
// §4 D.1). `xmin` (runtime concurrency token, см. CLAUDE.md §12) тоже не входит —
// он живёт в repo-leaf record, не в domain-структуре. Rules — order-sensitive
// (порядок rule-id в YC контракте значим). skill evgeniy §4 D.10.
func (s SecurityGroup) Equal(other SecurityGroup) bool {
	if s.ID != other.ID ||
		s.FolderID != other.FolderID ||
		s.NetworkID != other.NetworkID ||
		s.Name != other.Name ||
		s.Description != other.Description ||
		s.Status != other.Status ||
		s.DefaultForNetwork != other.DefaultForNetwork {
		return false
	}
	if !LabelsEqual(s.Labels, other.Labels) {
		return false
	}
	if len(s.Rules) != len(other.Rules) {
		return false
	}
	for i := range s.Rules {
		if !s.Rules[i].Equal(other.Rules[i]) {
			return false
		}
	}
	return true
}

// SecurityGroupRule — встроенное правило SG (Wave 2 batch B, KAC-94).
//
// Description — newtype `RcDescription` (skill evgeniy §4 D.2). Direction — enum
// `SecurityGroupRuleDirection` (skill §4 D.8). Остальные поля (Protocol/Ports/
// CidrBlocks/SecurityGroupID/PredefinedTarget) валидируются в service-слое —
// это сложные cross-field invariants.
//
// Note: Labels на rule-уровне остаётся `map[string]string`, не `RcLabels`.
// Причина: правила сериализуются как JSONB в колонке `security_groups.rules`,
// а `RcLabels` (`dict.HDict[K,V]`) использует embedded unexported map, который
// `encoding/json` не round-trip'ит. Валидация labels — через
// `ValidateLabels(LabelsFromMap(r.Labels))` в `Validate()`. На уровне
// `SecurityGroup.Labels` (отдельная JSONB-колонка `labels`) мы конвертим map
// ↔ RcLabels в repo (marshalJSONB(LabelsToMap(...))), а на rule-уровне эта
// двойная конверсия даёт лишнюю сложность без выгоды.
type SecurityGroupRule struct {
	ID             string
	Description    RcDescription
	Labels         map[string]string
	Direction      SecurityGroupRuleDirection
	FromPort       int64 // -1 = any
	ToPort         int64 // -1 = any
	ProtocolName   string
	ProtocolNumber int64
	V4CidrBlocks   []string
	V6CidrBlocks   []string
	// Для упрощения: только cidrBlocks; SG-target / predefined-target — TODO в следующей итерации.
	SecurityGroupID  string
	PredefinedTarget string
}

// Validate проверяет description/labels rule'а. Direction-семантика и
// CIDR/ports/protocol-валидации — в service-слое (validateSGRule).
func (r SecurityGroupRule) Validate() error {
	return multierr.Combine(
		r.Description.Validate(),
		ValidateLabels(LabelsFromMap(r.Labels)),
	)
}

// Equal — deep equality. Labels (map[string]string) — order-insensitive
// (map-семантика). V4CidrBlocks/V6CidrBlocks — order-sensitive (порядок CIDR
// в правиле формально не значим, но мы держимся order-sensitive для
// consistency с RouteTable.StaticRoutes и NIC.SecurityGroupIDs).
// skill evgeniy §4 D.10.
func (r SecurityGroupRule) Equal(other SecurityGroupRule) bool {
	return r.ID == other.ID &&
		r.Description == other.Description &&
		labelsMapEqual(r.Labels, other.Labels) &&
		r.Direction == other.Direction &&
		r.FromPort == other.FromPort &&
		r.ToPort == other.ToPort &&
		r.ProtocolName == other.ProtocolName &&
		r.ProtocolNumber == other.ProtocolNumber &&
		stringSlicesEqual(r.V4CidrBlocks, other.V4CidrBlocks) &&
		stringSlicesEqual(r.V6CidrBlocks, other.V6CidrBlocks) &&
		r.SecurityGroupID == other.SecurityGroupID &&
		r.PredefinedTarget == other.PredefinedTarget
}
