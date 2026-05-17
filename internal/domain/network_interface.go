package domain

import (
	"regexp"

	"go.uber.org/multierr"

	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
)

// NetworkInterface — first-class сетевой интерфейс (AWS-ENI-style; Wave 2 batch C, KAC-94).
//
// Семантически-нагруженные поля (Name/Description/Labels) — newtypes из
// `domain/types.go` со встроенным Validate(). `CreatedAt` сюда НЕ входит —
// DB-managed, живёт в `NetworkInterfaceRecord` (см. `domain/persistence.go`)
// согласно skill evgeniy §4 D.1 / §7 H.1.
//
// `Status` — enum `NetworkInterfaceStatus` (PROVISIONING/ACTIVE/AVAILABLE/
// FAILED/DELETING). skill §4 D.8.
//
// `MAC` остаётся `string` (это формат, а не семантическое имя ресурса) —
// валидируется через regex (`macAddressRe`) в `(NetworkInterface) Validate()`.
//
// `V4AddressIDs` / `V6AddressIDs` / `SecurityGroupIDs` остаются `[]string` —
// массивы reference-id, валидация — на уровне `corevalidate.ResourceID` в
// service-слое перед запросом к репо (cardinality-инвариант ≤1 v4 / ≤1 v6 —
// `validateNICAddressCardinality` в service + DB-level CHECK миграции 0018).
//
// Data-plane-проекция (NICDataplane: hv_id/sid/sid_seq/host_iface/netns/...) +
// резолвленные IP-строки (V4Addresses/V6Addresses) удалены в KAC-79/KAC-36
// (post-kube-ovn: kube-ovn управляет underlay сам, у kacho-vpc больше нет
// своей data-plane-проекции).
type NetworkInterface struct {
	ID          string
	ProjectID    string
	Name        RcNameVPC
	Description RcDescription
	Labels      RcLabels
	SubnetID    string
	// V4AddressIDs / V6AddressIDs — NIC ссылается на Address-ресурсы (kacho-vpc)
	// по id (epic KAC-2 / KAC-7). Один Address — максимум на одном NIC (enforced
	// сервис-слоем через addresses.used + referrer-tracking, см. service слой).
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
	// MAC — стабильный MAC-адрес интерфейса (AWS-ENI semantics): output-only,
	// аллоцируется при NetworkInterfaceService.Create, неизменен на жизни NIC
	// (Attach/Detach его не трогают), уникален в пределах всего облака. Формат:
	// lowercase, colon-separated, всегда 6 октетов; префикс `0e:` (locally
	// administered, unicast) зарезервирован под Kachō — все наши MAC начинаются
	// с него; остальные 5 байт — crypto/rand. См. internal/service/mac.go.
	MAC    string
	Status NetworkInterfaceStatus
}

// macAddressRe — каноническая форма MAC-адреса: lowercase, colon-separated,
// ровно 6 октетов (12 hex-символов). Допускается пустая строка (для legacy / pre-allocate
// контекста, когда MAC ещё не сгенерирован) — она пройдёт Validate().
var macAddressRe = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)

// Validate проверяет name/description/labels по domain-контракту + MAC-формат
// (если задан). Status валидируется отдельно — это enum, мы не отбиваем
// «unknown» значения здесь, потому что repo считывает legacy rows и должен
// маппить «STATUS_UNSPECIFIED» обратно в NIStatusUnspecified без error.
//
// Cross-resource invariants (subnet_id existence, address cardinality ≤1 v4/v6,
// security_group_ids existence) — service-слой (validateNICAddressCardinality
// + validateAddressRef). Это не newtype-level. skill evgeniy §4 D.4 / D.6.
func (n NetworkInterface) Validate() error {
	errs := []error{
		n.Name.Validate(),
		n.Description.Validate(),
		ValidateLabels(n.Labels),
	}
	if n.MAC != "" && !macAddressRe.MatchString(n.MAC) {
		errs = append(errs, coreerrors.InvalidArgument().
			AddFieldViolation("mac_address", "mac_address must match ^[0-9a-f]{2}(:[0-9a-f]{2}){5}$ (lowercase, colon-separated, 6 octets)").
			Err())
	}
	return multierr.Combine(errs...)
}

// Equal — deep equality по domain-полям. `CreatedAt` не входит (skill evgeniy
// §4 D.1). Reference-id массивы (V4AddressIDs/V6AddressIDs/SecurityGroupIDs) —
// order-sensitive: порядок задаётся сервис-слоем на Create/Update, его
// фиксируем для consistency. skill evgeniy §4 D.10.
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
