package service

import (
	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports/portmock"
)

// Общие fake-реализации port-интерфейсов и await-helper'ы вынесены в
// `internal/ports/portmock` (см. TODO #12) — раньше каждый service-/handler-
// test-файл держал свою копию. Здесь — тонкий shim с прежними именами, чтобы
// не переписывать тела тестов.

type (
	mockNetworkRepo  = portmock.NetworkRepo
	mockSubnetRepo   = portmock.SubnetRepo
	mockAddressRepo  = portmock.AddressRepo
	mockSGRepo       = portmock.SecurityGroupRepo
	mockOpsRepo      = portmock.OpsRepo
	mockZoneRegistry = portmock.ZoneRegistry
	testingT         = portmock.TestingT
)

var (
	newMockNetworkRepo  = portmock.NewNetworkRepo
	newMockSubnetRepo   = portmock.NewSubnetRepo
	newMockAddressRepo  = portmock.NewAddressRepo
	newMockSGRepo       = portmock.NewSecurityGroupRepo
	newMockOpsRepo      = portmock.NewOpsRepo
	newMockZoneRegistry = portmock.NewZoneRegistry
)

// newMockFolderClient — fake FolderClient; exists задаёт результат Exists().
func newMockFolderClient(exists bool) *portmock.FolderClient {
	return &portmock.FolderClient{OK: exists}
}

func awaitOpDone(t testingT, r *mockOpsRepo, opID string) *operations.Operation {
	return portmock.AwaitOpDone(t, r, opID)
}

func awaitAllOpsDone(t testingT, r *mockOpsRepo) { portmock.AwaitAllOpsDone(t, r) }
