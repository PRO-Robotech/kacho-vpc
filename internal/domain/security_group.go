// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package domain

// SecurityGroup — domain-сущность Security Group.
//
// Семантически-нагруженные поля (Name/Description/Labels) — newtypes из
// `domain/types.go` со встроенным Validate(). `CreatedAt` сюда НЕ входит —
// DB-managed, живет в `SecurityGroupRecord` (см.
// `internal/repo/kacho/entity_security_group.go`).
//
// `Rules` хранятся embedded (JSONB в БД); каждое правило — `SecurityGroupRule`
// с собственными newtype-полями (Description/Labels).
//
// `ID` / `ProjectID` / `NetworkID` — голый `string` (внешние reference-id;
// валидация — на уровне `corevalidate.ResourceID` в service-слое).
//
// Поля `Status` у SG нет: provisioning-lifecycle отсутствует, статус не
// наблюдался ни одним consumer'ом.
type SecurityGroup struct {
	ID                string
	ProjectID         string
	NetworkID         string
	Name              RcNameVPC
	Description       RcDescription
	Labels            RcLabels
	DefaultForNetwork bool
	Rules             []SecurityGroupRule
}

// Validate проверяет name/description/labels по domain-контракту. Вызывается
// use-case-слоем ПЕРЕД repo.Insert / repo.Update.
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
	return combineValidation(errs...)
}

// Equal — deep equality по domain-полям. `CreatedAt` не входит. `xmin` (runtime
// OCC-токен) тоже не входит — он живет в repo-leaf record, не в domain-структуре.
// Rules — order-sensitive (порядок rule-id в контракте значим).
func (s SecurityGroup) Equal(other SecurityGroup) bool {
	if s.ID != other.ID ||
		s.ProjectID != other.ProjectID ||
		s.NetworkID != other.NetworkID ||
		s.Name != other.Name ||
		s.Description != other.Description ||
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

// SecurityGroupRule — встроенное правило SG.
//
// Description — newtype `RcDescription`. Direction — enum
// `SecurityGroupRuleDirection`. Остальные поля (Protocol/Ports/CidrBlocks/
// SecurityGroupID/PredefinedTarget) валидируются в service-слое — это сложные
// cross-field invariants.
//
// Labels на rule-уровне остается `map[string]string`, не `RcLabels`.
// Причина: правила сериализуются как JSONB в колонке `security_groups.rules`,
// а `RcLabels` (`dict.HDict[K,V]`) использует embedded unexported map, который
// `encoding/json` не round-trip'ит. Валидация labels — через
// `ValidateLabels(LabelsFromMap(r.Labels))` в `Validate()`. На уровне
// `SecurityGroup.Labels` (отдельная JSONB-колонка `labels`) мы конвертим map
// ↔ RcLabels в repo (marshalJSONB(LabelsToMap(...))), а на rule-уровне эта
// двойная конверсия дает лишнюю сложность без выгоды.
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
	// Rule target — взаимоисключающие виды (proto oneof): cidr_blocks
	// (V4CidrBlocks/V6CidrBlocks), security_group_id (SG-target) или
	// predefined_target. SG-target (`SecurityGroupID`) разрешен ТОЛЬКО в пределах
	// той же Network, что и владеющая правилом SG (валидация —
	// `securitygroup.validateSGTargetSameNetwork`).
	SecurityGroupID  string
	PredefinedTarget string
}

// Validate проверяет description/labels rule'а. Direction-семантика и
// CIDR/ports/protocol-валидации — в service-слое (validateSGRule).
func (r SecurityGroupRule) Validate() error {
	return combineValidation(
		r.Description.Validate(),
		ValidateLabels(LabelsFromMap(r.Labels)),
	)
}

// Equal — deep equality. Labels (map[string]string) — order-insensitive
// (map-семантика). V4CidrBlocks/V6CidrBlocks — order-sensitive (порядок CIDR
// в правиле формально не значим, но мы держимся order-sensitive для
// consistency с RouteTable.StaticRoutes и NIC.SecurityGroupIDs).
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
