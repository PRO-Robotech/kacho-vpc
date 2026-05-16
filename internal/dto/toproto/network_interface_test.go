package toproto_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	// blank-import регистрирует трансферы (включая NetworkInterface).
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Wave 2 batch C (KAC-94): smoke-test трансфера NetworkInterfaceRecord →
// *vpcv1.NetworkInterface. Регистрация — в init() пакета toproto.
// Wave 5 replicate (KAC-94, NIC batch): NetworkInterfaceRecord уехал из
// domain в repo-leaf — тесты используют `kacho.NetworkInterfaceRecord`.

func TestDTO_TransferNetworkInterfaceRecord(t *testing.T) {
	at := time.Date(2026, 5, 15, 12, 34, 56, 789_000_000, time.UTC)
	rec := kachorepo.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{
			ID:               "e9bnic",
			FolderID:         "folder-x",
			Name:             domain.RcNameVPC("my-nic"),
			Description:      domain.RcDescription("desc"),
			Labels:           domain.LabelsFromMap(map[string]string{"env": "prod"}),
			SubnetID:         "e9bsub",
			V4AddressIDs:     []string{"e9baddrv4"},
			V6AddressIDs:     []string{"e9baddrv6"},
			SecurityGroupIDs: []string{"enpsg"},
			MAC:              "0e:1a:2b:3c:4d:5e",
			Status:           domain.NIStatusAvailable,
		},
		CreatedAt: at,
	}
	var dst *vpcv1.NetworkInterface
	require.NoError(t, dto.Transfer(dto.FromTo(rec, &dst)))
	require.NotNil(t, dst)
	assert.Equal(t, "e9bnic", dst.Id)
	assert.Equal(t, "folder-x", dst.FolderId)
	assert.Equal(t, "my-nic", dst.Name)
	assert.Equal(t, "desc", dst.Description)
	assert.Equal(t, "prod", dst.Labels["env"])
	assert.Equal(t, "e9bsub", dst.SubnetId)
	assert.Equal(t, []string{"e9baddrv4"}, dst.V4AddressIds)
	assert.Equal(t, []string{"e9baddrv6"}, dst.V6AddressIds)
	assert.Equal(t, []string{"enpsg"}, dst.SecurityGroupIds)
	assert.Equal(t, "0e:1a:2b:3c:4d:5e", dst.MacAddress)
	assert.Equal(t, vpcv1.NetworkInterface_AVAILABLE, dst.Status)
	require.NotNil(t, dst.CreatedAt)
	// truncate до seconds (verbatim YC).
	assert.Equal(t, at.Truncate(time.Second), dst.CreatedAt.AsTime())
}

// TestDTO_NetworkInterface_UsedByFilled — Reference{referrer{type,id}, type=USED_BY}
// заполняется только когда UsedByID != "" (parity со старым protoconv.NetworkInterface).
func TestDTO_NetworkInterface_UsedByFilled(t *testing.T) {
	rec := kachorepo.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{
			ID:         "e9bnic",
			SubnetID:   "e9bsub",
			MAC:        "0e:11:22:33:44:55",
			Status:     domain.NIStatusActive,
			UsedByType: "compute_instance",
			UsedByID:   "epdinst",
		},
	}
	var dst *vpcv1.NetworkInterface
	require.NoError(t, dto.Transfer(dto.FromTo(rec, &dst)))
	require.NotNil(t, dst.UsedBy)
	require.NotNil(t, dst.UsedBy.Referrer)
	assert.Equal(t, "compute_instance", dst.UsedBy.Referrer.Type)
	assert.Equal(t, "epdinst", dst.UsedBy.Referrer.Id)
}

func TestDTO_NetworkInterface_UsedByEmpty(t *testing.T) {
	rec := kachorepo.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{
			ID: "e9bnic", SubnetID: "e9bsub", MAC: "0e:11:22:33:44:55", Status: domain.NIStatusAvailable,
		},
	}
	var dst *vpcv1.NetworkInterface
	require.NoError(t, dto.Transfer(dto.FromTo(rec, &dst)))
	assert.Nil(t, dst.UsedBy)
}
