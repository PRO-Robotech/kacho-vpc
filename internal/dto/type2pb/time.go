// Package type2pb — реализации DTO трансферов domain/repo → proto.
// Skill evgeniy §3 C.4 (init-регистрация трансферов).
//
// Wave 2 pilot (KAC-99/KAC-94): на текущий момент зарегистрированы Network
// (repo-entity → vpcv1.Network) и time.Time → *timestamppb.Timestamp.
// Остальные ресурсы (Subnet/Address/RouteTable/SecurityGroup/Gateway/
// PrivateEndpoint/NetworkInterface) пока конвертируются через старый
// `internal/protoconv/protoconv.go`; они переедут в этот пакет в следующих
// итерациях (KAC-100..106).
package type2pb

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
)

// timeObj — нулевой struct-receiver для метода-трансфера time.Time → pb timestamp.
// Существует ради единого стиля «<resource>{}.toPb» (см. network.go), а не
// «свободная функция» — это упрощает grep по `\bnetwork\b{}.toPb` и т.п.
type timeObj struct{}

// toPb — truncate до секунд (verbatim YC `YC-DIFF-TIMESTAMP-PRECISION`).
// Nil-receiver для time.Time не имеет смысла (это value-тип, не pointer);
// «zero» time → timestamppb «zero» (1970-01-01). Caller проверяет
// `t.IsZero()` если хочет вернуть nil-pb-timestamp.
func (timeObj) toPb(t time.Time) (*timestamppb.Timestamp, error) {
	return timestamppb.New(t.Truncate(time.Second)), nil
}

func init() {
	dto.RegTransfer(dto.Fn2Face(timeObj{}.toPb))
}
