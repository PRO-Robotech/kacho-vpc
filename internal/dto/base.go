package dto

import (
	"reflect"

	"github.com/H-BF/corlib/pkg/dict"
	"github.com/pkg/errors"
)

type (
	// Interface -
	Interface[fromType any, toType any] interface {
		Transfer(fromType) (toType, error)
	}

	// NullTransfer -
	NullTransfer[fromType any, toType any] struct{}

	// DTO -
	DTO[tFrom any, tTo any] struct {
		from tFrom
		to   *tTo
	}

	// interfaceF -
	interfaceF[fromType any, toType any] func(fromType) (toType, error)

	tag[_ any, _ any] struct{}
)

// ErrDTO -
var ErrDTO = errors.New("data transfer failure")

var transfersReg dict.HDict[reflect.Type, any]

// Transfer -
func (NullTransfer[fromType, toType]) Transfer(src fromType) (ret toType, e error) {
	return ret, errors.WithMessagef(
		ErrDTO, "transfer type('%T') -> type('%T')", src, ret,
	)
}

// RegTransfer -
func RegTransfer[fromT any, toT any](impl Interface[fromT, toT]) {
	ty := reflect.TypeFor[tag[fromT, toT]]()
	transfersReg.Put(ty, impl)
}

// FindTransfer -
func FindTransfer[fromT any, toT any](tg tag[fromT, toT]) Interface[fromT, toT] {
	v, found := transfersReg.Get(
		reflect.TypeOf(tg),
	)
	if !found || v == nil {
		return NullTransfer[fromT, toT]{}
	}
	return v.(Interface[fromT, toT])
}

// Perform -
func (pf *DTO[tFrom, tTo]) Perform() (e error) {
	obj := FindTransfer(tag[tFrom, tTo]{})
	if pf.to == nil {
		return errors.WithMessage(ErrDTO, "destination object is null poiner")
	}
	*pf.to, e = obj.Transfer(pf.from)
	return e
}

// FromTo - делвет добро людям
func FromTo[tFrom any, tTo any](from tFrom, to *tTo) *DTO[tFrom, tTo] {
	return &DTO[tFrom, tTo]{
		from: from,
		to:   to,
	}
}

// Transfer -
func (f interfaceF[fromType, toType]) Transfer(arg fromType) (toType, error) {
	return f(arg)
}

// Fn2Face -
func Fn2Face[fromType any, toType any](fx func(fromType) (toType, error)) interfaceF[fromType, toType] {
	return fx
}
