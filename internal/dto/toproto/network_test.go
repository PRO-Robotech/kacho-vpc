package toproto_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	// blank-import регистрирует трансферы Network + time.
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Wave 2 pilot (KAC-99/KAC-94): убеждаемся, что dto.Transfer работает для
// зарегистрированных пар. Это smoke вокруг init()-цепочки.

func TestDTO_TransferNetworkRecord(t *testing.T) {
	at := time.Date(2026, 5, 15, 12, 34, 56, 789_000_000, time.UTC)
	rec := kachorepo.NetworkRecord{
		Network: domain.Network{
			ID:                     "enp1",
			FolderID:               "folder-x",
			Name:                   domain.RcNameVPC("my-net"),
			Description:            domain.RcDescription("desc"),
			Labels:                 domain.LabelsFromMap(map[string]string{"env": "prod"}),
			DefaultSecurityGroupID: "enpsg",
		},
		CreatedAt: at,
	}
	var dst *vpcv1.Network
	require.NoError(t, dto.Transfer(dto.FromTo(rec, &dst)))
	require.NotNil(t, dst)
	assert.Equal(t, "enp1", dst.Id)
	assert.Equal(t, "folder-x", dst.FolderId)
	assert.Equal(t, "my-net", dst.Name)
	assert.Equal(t, "desc", dst.Description)
	assert.Equal(t, "prod", dst.Labels["env"])
	assert.Equal(t, "enpsg", dst.DefaultSecurityGroupId)
	require.NotNil(t, dst.CreatedAt)
	// truncate до seconds (verbatim YC).
	assert.Equal(t, at.Truncate(time.Second), dst.CreatedAt.AsTime())
}
