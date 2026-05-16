package domain

import "go.uber.org/multierr"

// Network — сетевой ресурс (Wave 2 pilot, KAC-99/KAC-94).
//
// Поля семантически-нагруженных колонок — newtypes (Name/Description/Labels)
// со встроенным Validate(). `CreatedAt` сюда НЕ входит — это DB-managed
// (DEFAULT now()) и живёт в repo-сущности (см. internal/repo/network_repo.go,
// type repoNetwork) согласно skill evgeniy §4 D.1 / §7 H.1.
//
// `ID` / `FolderID` / `DefaultSecurityGroupID` — остаются голым `string`, это
// внешние reference-id (newtype добавит шум без выгоды; их валидация — на уровне
// `corevalidate.ResourceID` в service-слое перед запросом к репо).
type Network struct {
	ID                     string
	FolderID               string
	Name                   RcNameVPC
	Description            RcDescription
	Labels                 RcLabels
	DefaultSecurityGroupID string
}

// Validate проверяет все семантически-нагруженные поля Network по domain-
// контракту (verbatim YC VPC permissive policy + label cardinality / key /
// value regex). Возвращает gRPC `InvalidArgument` с FieldViolation, либо nil.
//
// Вызывается use-case-слоем ПЕРЕД repo.Insert / repo.Update — domain
// становится единственным источником правды о валидности (skill evgeniy
// §4 D.4 / D.6). Старая цепочка `corevalidate.NameVPC/Description/Labels`
// в service-слое в Wave 2 для Network удалена.
func (n Network) Validate() error {
	return multierr.Combine(
		n.Name.Validate(),
		n.Description.Validate(),
		ValidateLabels(n.Labels),
	)
}

// Equal — deep equality по domain-полям. `CreatedAt` сюда не входит (он в
// repo-leaf Record, см. skill evgeniy §4 D.1). Используется для noop-detection
// в Update-flow и для testing-equality в use-case тестах. skill evgeniy §4 D.10.
func (n Network) Equal(other Network) bool {
	return n.ID == other.ID &&
		n.FolderID == other.FolderID &&
		n.Name == other.Name &&
		n.Description == other.Description &&
		LabelsEqual(n.Labels, other.Labels) &&
		n.DefaultSecurityGroupID == other.DefaultSecurityGroupID
}
