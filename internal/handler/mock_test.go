package handler

import (
	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// Общие fake-реализации port-интерфейсов и await-helper'ы вынесены в
// `internal/repo/repomock` (см. TODO #12) — раньше каждый handler-/service-
// test-файл держал свою копию. Здесь — тонкий shim с прежними именами, чтобы
// не переписывать тела тестов.

type (
	mockOpsRepo = repomock.OpsRepo
	testingT    = repomock.TestingT
)

var (
	newMockNetworkRepo      = repomock.NewNetworkRepo
	newMockSubnetRepoForSvc = repomock.NewSubnetRepo
	newMockSGRepoForSvc     = repomock.NewSecurityGroupRepo
	newMockOpsRepo          = repomock.NewOpsRepo
)

// newMockFolderClient — fake FolderClient; exists задаёт результат Exists().
func newMockFolderClient(exists bool) *repomock.FolderClient {
	return &repomock.FolderClient{OK: exists}
}

func awaitOpDone(t testingT, r *mockOpsRepo, opID string) *operations.Operation {
	return repomock.AwaitOpDone(t, r, opID)
}
