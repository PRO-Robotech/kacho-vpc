// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package domain

import "regexp"

// NetworkInterface — самостоятельный сетевой интерфейс, отвязанный от Instance.
//
// Семантически-нагруженные поля (Name/Description/Labels) — newtypes из
// `domain/types.go` со встроенным Validate(). `CreatedAt` сюда НЕ входит —
// DB-managed, живет в `NetworkInterfaceRecord` (см.
// `internal/repo/kacho/entity_network_interface.go`).
//
// `Status` — enum `NetworkInterfaceStatus` (PROVISIONING/ACTIVE/AVAILABLE/
// FAILED/DELETING).
//
// `MAC` остается `string` (это формат, а не семантическое имя ресурса) —
// валидируется через regex (`macAddressRe`) в `(NetworkInterface) Validate()`.
//
// `V4AddressIDs` / `V6AddressIDs` / `SecurityGroupIDs` остаются `[]string` —
// массивы reference-id, валидация — на уровне `corevalidate.ResourceID` в
// service-слое перед запросом к репо (cardinality-инвариант ≤1 v4 / ≤1 v6 —
// `validateNICAddressCardinality` в service + DB-level CHECK).
//
// NetworkInterface — чисто control-plane-проекция: только tenant-facing поля
// (id/subnet/addresses/security-groups/used_by/mac/status). Никаких
// инфра/data-plane-полей у kacho-vpc нет — underlay-программирование живет в
// будущем sibling-сервисе kacho-vpc-implement.
type NetworkInterface struct {
	ID          string
	ProjectID   string
	Name        RcNameVPC
	Description RcDescription
	Labels      RcLabels
	SubnetID    string
	// V4AddressIDs / V6AddressIDs — NIC ссылается на Address-ресурсы (kacho-vpc)
	// по id. Один Address — максимум на одном NIC (enforced сервис-слоем через
	// addresses.used + referrer-tracking, см. service слой).
	V4AddressIDs     []string
	V6AddressIDs     []string
	SecurityGroupIDs []string
	// UsedBy* — денормализованная "кто приаттачил этот NIC" ссылка (зеркало
	// Address.used_by; e.g. {compute_instance, <instance_id>}). Выставляется
	// AttachToInstance, очищается DetachFromInstance. Один референт на NIC —
	// поэтому храним flat-колонками прямо на network_interfaces (а не отдельной
	// таблицей, как address_references).
	UsedByType string
	UsedByID   string
	UsedByName string
	// MAC — стабильный MAC-адрес интерфейса: output-only, аллоцируется при
	// NetworkInterfaceService.Create, неизменен на жизни NIC (Attach/Detach его
	// не трогают), уникален в пределах всего облака. Формат:
	// lowercase, colon-separated, всегда 6 октетов; префикс `0e:` (locally
	// administered, unicast) зарезервирован под Kachō — все наши MAC начинаются
	// с него; остальные 5 байт — crypto/rand. См.
	// internal/apps/kacho/shared/macutil/mac.go.
	MAC    string
	Status NetworkInterfaceStatus
}

// macAddressRe — каноническая форма MAC-адреса: lowercase, colon-separated,
// ровно 6 октетов (12 hex-символов). Пустая строка допустима (MAC еще не
// сгенерирован) — она пройдет Validate().
var macAddressRe = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)

// Validate проверяет name/description/labels по domain-контракту + MAC-формат
// (если задан). Status здесь не валидируется — это enum, и «unknown» значения мы
// не отбиваем, потому что repo обязан маппить «STATUS_UNSPECIFIED» обратно в
// NIStatusUnspecified без error.
//
// Cross-resource invariants — не newtype-level, живут выше по стеку:
//   - subnet_id existence + address cardinality (≤1 v4/v6) + address existence —
//     service-слой (validateNICAddressCardinality + validateAddressRef / attach-CAS)
//     с DB-backstop (FK subnet_id RESTRICT, CHECK ≤1, address_references CAS);
//   - security_group_ids existence — НЕ энфорсится: jsonb-массив без FK/join-table,
//     within-service refcheck отсутствует (в отличие от v4/v6 address_ids здесь нет
//     backstop). SG.Delete не блокируется ссылающимся NIC → возможен dangling ref.
//     Known gap PRO-Robotech/kacho-vpc#27 (red-тест SG-DEL-NEG-NIC-ATTACHED); фикс —
//     DB-level join-table + FK RESTRICT (rule #10) отдельным behavioral-PR.
func (n NetworkInterface) Validate() error {
	errs := []error{
		n.Name.Validate(),
		n.Description.Validate(),
		ValidateLabels(n.Labels),
	}
	if n.MAC != "" && !macAddressRe.MatchString(n.MAC) {
		errs = append(errs, newValidationError("mac_address", "mac_address must match ^[0-9a-f]{2}(:[0-9a-f]{2}){5}$ (lowercase, colon-separated, 6 octets)"))
	}
	return combineValidation(errs...)
}

// Equal — deep equality по domain-полям. `CreatedAt` не входит. Reference-id
// массивы (V4AddressIDs/V6AddressIDs/SecurityGroupIDs) — order-sensitive:
// порядок задается сервис-слоем на Create/Update, его фиксируем для consistency.
func (n NetworkInterface) Equal(other NetworkInterface) bool {
	return n.ID == other.ID &&
		n.ProjectID == other.ProjectID &&
		n.Name == other.Name &&
		n.Description == other.Description &&
		LabelsEqual(n.Labels, other.Labels) &&
		n.SubnetID == other.SubnetID &&
		stringSlicesEqual(n.V4AddressIDs, other.V4AddressIDs) &&
		stringSlicesEqual(n.V6AddressIDs, other.V6AddressIDs) &&
		stringSlicesEqual(n.SecurityGroupIDs, other.SecurityGroupIDs) &&
		n.UsedByType == other.UsedByType &&
		n.UsedByID == other.UsedByID &&
		n.UsedByName == other.UsedByName &&
		n.MAC == other.MAC &&
		n.Status == other.Status
}
