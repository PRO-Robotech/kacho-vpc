// Package dto — table-driven generic-based DTO трансферы. Skill evgeniy §3
// (C.1–C.6) и §11 AP-11: запрет на «прямые маппинг-функции» вроде
// `toproto.Network(d)` без регистрации в DTO-реестре.
//
// Структура:
//   - dto/base.go (этот файл): generic Interface, RegTransfer / FindTransfer,
//     FromTo helper, Transfer entry-point с type-set generic constraint.
//   - dto/toproto/*.go: реализации Interface[domain.X, *vpcv1.X] + init()-
//     регистрация в реестре.
//
// Use-case в caller-site:
//
//	var dst *vpcv1.Network
//	if err := dto.Transfer(dto.FromTo(rec, &dst)); err != nil { ... }
//	return anypb.New(dst)
//
// Wave 2 (KAC-94): trans-реестр содержит все 8 VPC-ресурсов
// (Network/Subnet/Address/RouteTable/SecurityGroup/Gateway/PrivateEndpoint/
// NetworkInterface) + time.Time — см. dto/toproto/.
//
// Wave 5 (KAC-94, skill evgeniy §4 D.1 / §11 AP-11): `NetworkRecord` уехал
// из `domain` в repo-leaf `internal/repo/kacho/entity_network.go` — type-set
// ниже для Network ссылается на `kacho.NetworkRecord`. Legacy helper
// `protoconv.Network` удалён вместе с пакетом `internal/protoconv/` —
// handler-test переписан на `dto.Transfer(dto.FromTo(rec, &dst))`.
package dto

import (
	"fmt"
	"reflect"
	"sync"
	"time"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	pepb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Interface — generic transfer-функтор F → T. Реализация живёт в подпакете
// dto/toproto/ (или dto/pb2type/) и регистрируется в реестре через
// RegTransfer.
type Interface[F any, T any] interface {
	Transfer(F) (T, error)
}

// Fn — adapter: обычная Go-функция как Interface. Удобство для регистрации
// без объявления отдельной struct-pair'ы под каждое маппинг-методом.
type Fn[F any, T any] func(F) (T, error)

// Transfer — реализация Interface для Fn.
func (f Fn[F, T]) Transfer(src F) (T, error) { return f(src) }

// Fn2Face оборачивает функцию в Interface — синтаксический helper для init():
//
//	dto.RegTransfer(dto.Fn2Face(network{}.toPb))
func Fn2Face[F any, T any](fn func(F) (T, error)) Interface[F, T] { return Fn[F, T](fn) }

// ---- Registry ----------------------------------------------------------------

// tag — type-level marker для индексирования реестра по паре (F, T) через
// reflect.TypeFor. Сам value никогда не существует, нужен только для типа.
type tag[_ any, _ any] struct{}

var (
	// regMu защищает transfersReg от конкурентного доступа.
	regMu sync.RWMutex
	// transfersReg — реестр трансферов F→T, индексированный по reflect.TypeFor[tag[F,T]].
	transfersReg = map[reflect.Type]any{}
)

// RegTransfer регистрирует трансфер F → T под ключом reflect.TypeFor[tag[F,T]].
// Дубликат регистрации (та же пара (F,T)) — panic в init().
func RegTransfer[F any, T any](impl Interface[F, T]) {
	key := reflect.TypeFor[tag[F, T]]()
	regMu.Lock()
	defer regMu.Unlock()
	if _, ok := transfersReg[key]; ok {
		panic(fmt.Sprintf("dto: duplicate transfer registration for %s", key.String()))
	}
	transfersReg[key] = impl
}

// findTransfer достаёт зарегистрированный Interface[F,T] из реестра, либо
// (nil, false) если такой пары нет.
func findTransfer[F any, T any]() (Interface[F, T], bool) {
	key := reflect.TypeFor[tag[F, T]]()
	regMu.RLock()
	defer regMu.RUnlock()
	v, ok := transfersReg[key]
	if !ok {
		return nil, false
	}
	impl, ok := v.(Interface[F, T])
	if !ok {
		return nil, false
	}
	return impl, true
}

// ---- DTO entry-point (Transfer + FromTo) -------------------------------------

// DTO — pair-объект, который собирает FromTo(): хранит src + указатель на
// dst и реализует Perform() через registry-lookup. Поле dst — pointer-to-T,
// чтобы caller получил результат через свой собственный nil-pointer.
type DTO[F any, T any] struct {
	src F
	dst *T
}

// Perform выполняет лукап Interface[F,T] и пишет результат в *dto.dst.
// Ошибки: ErrTransferNotRegistered если пары нет; пробрасывает ошибку реализации.
func (d *DTO[F, T]) Perform() error {
	impl, ok := findTransfer[F, T]()
	if !ok {
		var f F
		var t T
		return fmt.Errorf("dto: no transfer registered for %T → %T", f, t)
	}
	res, err := impl.Transfer(d.src)
	if err != nil {
		return err
	}
	*d.dst = res
	return nil
}

// FromTo — конструктор DTO. Применяется в caller-site:
//
//	dto.Transfer(dto.FromTo(rec, &dst))
//
// Возвращает *DTO[F,T] — пара указатель для Transfer, чьё имплицитное
// поведение видно компилятором через type-set constraint (см. Transfer).
func FromTo[F any, T any](src F, dst *T) *DTO[F, T] {
	return &DTO[F, T]{src: src, dst: dst}
}

// Transferrable — закрытый sum-type generic constraint для Transfer():
// принимает только те *DTO[F,T] пары, которые **явно** зарегистрированы в
// type-set ниже. Это даёт compile-time гарантию: попытка вызвать
// `dto.Transfer(dto.FromTo(someUnregisteredSrc, &dst))` с парой (F,T), не
// перечисленной в union — провалится в compile-time, а не во время выполнения
// через ErrTransferNotRegistered.
//
// Skill evgeniy §3 C.5 (соответствует `types2ProtoVariants` в исходной spec):
// type-set генерик-constraint над union допустимых пар. Wave 2 (KAC-94) —
// зафиксирован для всех 8 VPC-ресурсов + time.Time.
//
// Расширение: добавление нового ресурса в DTO-реестр требует одновременно
// (а) новой `*DTO[domain.<X>Record, *<protopb>.<X>]` пары в union ниже,
// (б) нового `init()` с `dto.RegTransfer(dto.Fn2Face(<x>{}.toPb))` в
// `internal/dto/toproto/`. Без обоих изменений код не скомпилируется.
type Transferrable interface {
	Perform() error

	*DTO[time.Time, *timestamppb.Timestamp] |
		*DTO[kachorepo.NetworkRecord, *vpcv1.Network] |
		*DTO[kachorepo.SubnetRecord, *vpcv1.Subnet] |
		*DTO[kachorepo.AddressRecord, *vpcv1.Address] |
		*DTO[kachorepo.RouteTableRecord, *vpcv1.RouteTable] |
		*DTO[kachorepo.SecurityGroupRecord, *vpcv1.SecurityGroup] |
		*DTO[kachorepo.GatewayRecord, *vpcv1.Gateway] |
		*DTO[kachorepo.PrivateEndpointRecord, *pepb.PrivateEndpoint] |
		*DTO[kachorepo.NetworkInterfaceRecord, *vpcv1.NetworkInterface]
}

// Transfer запускает Perform() на dto. Это единственная публичная entry-point.
//
// Skill evgeniy §3 C.5: `Transfer[V types2ProtoVariants]` — generic constraint
// над type-set допустимых пар. С Wave 2 (KAC-94) type-set закрыт (см.
// `Transferrable` выше) — допустимые пары фиксируются compile-time.
func Transfer[V Transferrable](dto V) error {
	return dto.Perform()
}
