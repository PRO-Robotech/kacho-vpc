package handler

import (
	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports/portmock"
)

// Общие fake-реализации port-интерфейсов и await-helper'ы вынесены в
// `internal/ports/portmock` (см. TODO #12) — раньше каждый handler-/service-
// test-файл держал свою копию. Здесь — тонкий shim с прежними именами, чтобы
// не переписывать тела тестов.

type (
	mockOpsRepo = portmock.OpsRepo
	testingT    = portmock.TestingT
)

var (
	newMockNetworkRepo          = portmock.NewNetworkRepo
	newMockSubnetRepoForSvc     = portmock.NewSubnetRepo
	newMockRouteTableRepoForSvc = portmock.NewRouteTableRepo
	newMockSGRepoForSvc         = portmock.NewSecurityGroupRepo
	newMockGatewayRepoForSvc    = portmock.NewGatewayRepo
	newMockPERepoForSvc         = portmock.NewPrivateEndpointRepo
	newMockOpsRepo              = portmock.NewOpsRepo
)

// newMockFolderClient — fake FolderClient; exists задаёт результат Exists().
func newMockFolderClient(exists bool) *portmock.FolderClient {
	return &portmock.FolderClient{OK: exists}
}

func awaitOpDone(t testingT, r *mockOpsRepo, opID string) *operations.Operation {
	return portmock.AwaitOpDone(t, r, opID)
}
