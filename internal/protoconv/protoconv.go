// Package protoconv — единственное место конверсии domain-сущностей VPC в proto-сообщения.
//
// Зачем: раньше каждый ресурс имел ДВЕ копии конвертера — `domainXToProto` в
// service-слое (для `Operation.response`) и `xToProto` в handler-слое (для Get/List).
// Копии разъехались: handler-версии ставили `created_at` (truncate до секунд),
// service-версии — нет → клиент, читающий `Operation.response`, получал `created_at == null`,
// а тот же ресурс через Get — с заполненным `created_at` (расхождение с verbatim YC).
// Теперь конвертер один; и service, и handler зовут `protoconv.X(...)`.
//
// Контракт: `created_at` всегда truncate до секунд (verbatim YC — `YC-DIFF-TIMESTAMP-PRECISION`).
package protoconv

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

func ts(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t.Truncate(time.Second)) }

// Network конвертирует repo-entity Network → vpcv1.Network.
//
// Wave 2 pilot (KAC-99/KAC-94): принимает *domain.NetworkRecord (repo-entity
// с CreatedAt), а не *domain.Network — CreatedAt уехал в repo-projection
// (skill evgeniy §4 D.1 / §7 H.1). Это legacy-функция, оставлена ради тестов
// (handler_test и т.п.). Новый production-call идёт через DTO-реестр
// (`dto.Transfer(dto.FromTo(...))`) в `service/network.go::marshalNetworkRecord`
// и `handler/network_handler.go::networkToPb`. См. skill §3 C.3 / AP-11.
func Network(rec *domain.NetworkRecord) *vpcv1.Network {
	return &vpcv1.Network{
		Id:                     rec.ID,
		FolderId:               rec.FolderID,
		CreatedAt:              ts(rec.CreatedAt),
		Name:                   string(rec.Name),
		Description:            string(rec.Description),
		Labels:                 domain.LabelsToMap(rec.Labels),
		DefaultSecurityGroupId: rec.DefaultSecurityGroupID,
	}
}

// Subnet/Address/RouteTable конверторы перенесены в `internal/dto/type2pb/`
// (Wave 2 batch A, KAC-94, skill evgeniy §3 C.6 / AP-11). См. вызов через
// `dto.Transfer(dto.FromTo(...))` в service/handler.
//
// SecurityGroup/Gateway/PrivateEndpoint конверторы перенесены в `internal/dto/type2pb/`
// (Wave 2 batch B, KAC-94). Старые функции `protoconv.SecurityGroup` /
// `protoconv.Gateway` / `protoconv.PrivateEndpoint` удалены — все вызовы идут
// через `dto.Transfer(dto.FromTo(record, &dst))`.
//
// NetworkInterface конвертер перенесён в `internal/dto/type2pb/network_interface.go`
// (Wave 2 batch C, KAC-94). Старая `protoconv.NetworkInterface` удалена — все
// вызовы идут через `dto.Transfer(dto.FromTo(record, &dst))`.
