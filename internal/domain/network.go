package domain

import (
	"time"

	"github.com/H-BF/corlib/pkg/dict"
	"github.com/H-BF/corlib/pkg/option"
)

// Network — сетевой ресурс.
type Network struct {
	ID        string
	FolderID  string
	CreatedAt time.Time //<- сомневаюсь что это поле нужно в доменной модели
	Name      string
	//        ^^^^^ -> RcNameOpt см ниже
	// PROTO; Value must match the regular expression ``\|[a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?``.
	Description string
	//          ^^^^^^ --> нужно RcDescription  см ниже
	// PROTO: Optional description of the network. 0-256 characters long.
	Labels map[string]string
	//     ^^^^^^^^^^^^^^^^ -> RcLabels см ниже
	//              PROTO:
	// Resource labels as `key:value` pairs.
	// No more than 64 per resource.
	// The maximum string length in characters for each value is 63.
	// Each value must match the regular expression `[-_0-9a-z]*`.
	// The string length in characters for each key must be 1-63.
	// Each key must match the regular expression `[a-z][-_0-9a-z]*`.
	// - из этого сдедует - нужна валидация
	DefaultSecurityGroupID string
}

// для всех типов из доменной модели нужна валидация и сравнение

func (nv Network) Validate() error {
	return nil // need impl
}

type (
	LabelKey string

	LabelVal string

	RcLabels = dict.HDict[LabelKey, LabelVal]

	RcName string

	RcDescription string

	RcNameOpt = option.ValueOf[RcName]
)

func (LabelKey) Validate() error {
	return nil //need impl
}

func (LabelVal) Validate() error {
	return nil //need impl
}

func (RcName) Validate() error {
	return nil //need impl
}

func (RcDescription) Validate() error {
	return nil //need impl
}

/*// NOTES: зачем валидация в доменной модели
    - потому что доменная модель определяет бизнес модель всего приклада
	  и ее валидация является источником правильности сущностей в приложении
	- обычно происходит такой флоу CALL gRPC-service ( proto-query); DTO(proto->domain); domain->validate; mutate-in-DB
*/
