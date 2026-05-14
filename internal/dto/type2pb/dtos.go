package type2pb

import (
	"errors"
	"time"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.uber.org/multierr"
)

/*//
табличные преобразования из Type to protobuf-Type
в Transfer могут быть переданы только строго определенные варианты преобразований
иначе компилятор выдаст ошибку
*/

// Transfer -
func Transfer[v types2ProtoVariants](obj v) error {
	err := obj.Perform()
	if err != nil && !errors.Is(err, dto.ErrDTO) {
		err = multierr.Append(err, dto.ErrDTO)
	}
	return err
}

type types2ProtoVariants interface {
	Perform() error
	*dto.DTO[domain.Network, *vpcv1.Network] |
		*dto.DTO[time.Time, *timestamppb.Timestamp]
}

type (
	network struct{}
	timeObj struct{}
)

func (timeObj) toPb(t time.Time) (*timestamppb.Timestamp, error) {
	return timestamppb.New(t.Truncate(time.Second)), nil
}

func (network) toPb(n domain.Network) (*vpcv1.Network, error) {
	ret := &vpcv1.Network{
		Id:                     n.ID,
		FolderId:               n.FolderID,
		Name:                   n.Name,
		Description:            n.Description,
		Labels:                 n.Labels,
		DefaultSecurityGroupId: n.DefaultSecurityGroupID,
	}
	if err := Transfer(dto.FromTo(n.CreatedAt, &ret.CreatedAt)); err != nil {
		return nil, err
	}
	return ret, nil
}

func init() {
	dto.RegTransfer(dto.Fn2Face(network{}.toPb))
	dto.RegTransfer(dto.Fn2Face(timeObj{}.toPb))
}

func exampleUsage() {
	var src domain.Network
	var dst *vpcv1.Network
	err := Transfer(dto.FromTo(src, &dst))
	if err != nil {
		_ = err // fail handler
	}
}
