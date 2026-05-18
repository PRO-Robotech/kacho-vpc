package toproto

import (
	reference "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/reference"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// address — receiver-объект под трансфер kacho.AddressRecord → *vpcv1.Address.
// Wave 5 replicate (KAC-94): AddressRecord уехал из domain в repo-leaf;
// parity с network.go (network{}.toPb).
type address struct{}

// toPb формирует *vpcv1.Address из repo-entity. CreatedAt — truncate до секунд.
//
// oneof Address (External/Internal v4/v6) выбирается по тому, какое из specs
// заполнено. Семантика идентична legacy protoconv.Address.
func (address) toPb(rec kachorepo.AddressRecord) (*vpcv1.Address, error) {
	ts, err := (timeObj{}).toPb(rec.CreatedAt)
	if err != nil {
		return nil, err
	}
	p := &vpcv1.Address{
		Id:                 rec.ID,
		ProjectId:          rec.ProjectID,
		CreatedAt:          ts,
		Name:               string(rec.Name),
		Description:        string(rec.Description),
		Labels:             domain.LabelsToMap(rec.Labels),
		Reserved:           rec.Reserved,
		Used:               rec.Used,
		Type:               vpcv1.Address_Type(rec.Type),
		IpVersion:          vpcv1.Address_IpVersion(rec.IpVersion),
		DeletionProtection: rec.DeletionProtection,
	}
	switch {
	case rec.ExternalIpv4 != nil:
		ext := &vpcv1.ExternalIpv4Address{
			Address: rec.ExternalIpv4.Address,
			ZoneId:  rec.ExternalIpv4.ZoneID,
		}
		if rec.ExternalIpv4.Requirements != nil {
			ext.Requirements = &vpcv1.AddressRequirements{
				DdosProtectionProvider: rec.ExternalIpv4.Requirements.DdosProtectionProvider,
				OutgoingSmtpCapability: rec.ExternalIpv4.Requirements.OutgoingSmtpCapability,
			}
		}
		p.Address = &vpcv1.Address_ExternalIpv4Address{ExternalIpv4Address: ext}
	case rec.ExternalIpv6 != nil:
		ext6 := &vpcv1.ExternalIpv6Address{
			Address: rec.ExternalIpv6.Address,
			ZoneId:  rec.ExternalIpv6.ZoneID,
		}
		if rec.ExternalIpv6.Requirements != nil {
			ext6.Requirements = &vpcv1.AddressRequirements{
				DdosProtectionProvider: rec.ExternalIpv6.Requirements.DdosProtectionProvider,
				OutgoingSmtpCapability: rec.ExternalIpv6.Requirements.OutgoingSmtpCapability,
			}
		}
		p.Address = &vpcv1.Address_ExternalIpv6Address{ExternalIpv6Address: ext6}
	case rec.InternalIpv6 != nil:
		p.Address = &vpcv1.Address_InternalIpv6Address{
			InternalIpv6Address: &vpcv1.InternalIpv6Address{
				Address: rec.InternalIpv6.Address,
				Scope:   &vpcv1.InternalIpv6Address_SubnetId{SubnetId: rec.InternalIpv6.SubnetID},
			},
		}
	case rec.InternalIpv4 != nil:
		p.Address = &vpcv1.Address_InternalIpv4Address{
			InternalIpv4Address: &vpcv1.InternalIpv4Address{
				Address: rec.InternalIpv4.Address,
				Scope:   &vpcv1.InternalIpv4Address_SubnetId{SubnetId: rec.InternalIpv4.SubnetID},
			},
		}
	}
	// used_by (kacho extension, output-only) — кто использует адрес.
	for _, ref := range rec.UsedBy {
		if ref == nil {
			continue
		}
		p.UsedBy = append(p.UsedBy, &reference.Reference{
			Referrer: &reference.Referrer{Type: ref.ReferrerType, Id: ref.ReferrerID},
			Type:     reference.Reference_USED_BY,
		})
	}
	return p, nil
}

func init() {
	dto.RegTransfer(dto.Fn2Face(address{}.toPb))
}
