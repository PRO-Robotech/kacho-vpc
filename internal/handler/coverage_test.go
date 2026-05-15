package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// Этот файл расширяет handler-тесты, покрывая методы Update/Move/ListOperations/Delete
// для всех публичных ресурсов. Сценарии заимствованы из Postman-коллекции
// (collections/kacho-vpc.postman_collection.json) — case-id'ы NET-*, SUB-*,
// ADR-*, RT-*, SG-*, GW-*. Покрывает sync InvalidArgument paths (наиболее
// частый сценарий валидации до Operation worker'а). Fake-реализации port-ов —
// в `internal/ports/portmock` (shim — в mock_test.go).

// ---- Network handler — additional coverage ----
//
// Wave 3a pilot (KAC-94): Network-handler-тесты переехали в
// `internal/apps/kacho/api/network/usecase_test.go` (NetworkHandler удалён,
// Handler теперь живёт в use-case-пакете).

// ---- Subnet handler — additional coverage ----

func TestSubnetHandler_Update_InvalidArg(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	or := newMockOpsRepo()
	h := NewSubnetHandler(svc.NewSubnetService(sr, newMockNetworkRepo(), newMockFolderClient(true), or, nil))
	_, err := h.Update(context.Background(), &vpcv1.UpdateSubnetRequest{SubnetId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetHandler_ListOperations_RequiresID(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	or := newMockOpsRepo()
	h := NewSubnetHandler(svc.NewSubnetService(sr, newMockNetworkRepo(), newMockFolderClient(true), or, nil))
	_, err := h.ListOperations(context.Background(), &vpcv1.ListSubnetOperationsRequest{SubnetId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- Address handler — moved to internal/apps/kacho/api/address/usecase_test.go (Wave 3, KAC-94) ----
// TestAddressHandler_Update_InvalidArg / ListOperations_RequiresID → TestHandler_Update_InvalidArg
// / TestHandler_ListOperations_RequiresID в новом пакете.

// ---- RouteTable handler — moved to internal/apps/kacho/api/routetable/usecase_test.go (Wave 3b) ----

// ---- SecurityGroup handler — moved to internal/apps/kacho/api/securitygroup/usecase_test.go (Wave 3) ----

// ---- Gateway handler — moved to internal/apps/kacho/api/gateway/usecase_test.go (Wave 3b) ----
