// Package dto — table-driven generic-based DTO трансферы. Skill evgeniy §3
// (C.1–C.6) и §11 AP-11: запрет на «прямые маппинг-функции» вроде
// `protoconv.Network(d)` без регистрации в DTO-реестре.
//
// Структура:
//   - dto/base.go (этот файл): generic Interface, RegTransfer / FindTransfer,
//     FromTo helper, Transfer entry-point с type-set generic constraint.
//   - dto/type2pb/*.go: реализации Interface[domain.X, *vpcv1.X] + init()-
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
// NetworkInterface) + time.Time — см. dto/type2pb/. `protoconv.X(...)`
// удалены, остался только legacy helper `protoconv.Network` для одного
// handler_test (будет удалён в следующей фазе).
package dto

import (
	"fmt"
	"reflect"
	"sync"
)

// Interface — generic transfer-функтор F → T. Реализация живёт в подпакете
// dto/type2pb/ (или dto/pb2type/) и регистрируется в реестре через
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
	regMu        sync.RWMutex
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

// Transferrable — type-set constraint для Transfer(): принимает только те
// *DTO[F,T] пары, для которых вызов имеет Perform()-метод. На pilot-стадии
// (только Network + time.Time) этот constraint допускает любую *DTO[F,T] —
// сужающий type-set из PR #52 (`*DTO[domain.Network, *vpcv1.Network] | ...`)
// добавит compile-time-гарантии когда DTO-реестр стабилизируется (Wave 3).
type Transferrable interface {
	Perform() error
}

// Transfer запускает Perform() на dto. Это единственная публичная entry-point.
//
// Skill evgeniy §3 C.5: `Transfer[v types2ProtoVariants]` — generic constraint
// над type-set допустимых пар. На pilot мы оставляем constraint открытым
// (`Transferrable`) ради экономии boilerplate; точный набор пар (sum-type)
// будет зафиксирован в `dto/type2pb/dtos.go` когда мигрируют все 8 ресурсов.
func Transfer[V Transferrable](dto V) error {
	return dto.Perform()
}
