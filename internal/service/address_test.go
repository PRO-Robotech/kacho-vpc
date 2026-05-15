package service

import (
	"context"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// Wave 3 (KAC-94): AddressService переехал в
// `internal/apps/kacho/api/address/` (use-case-структура, skill evgeniy §2).
// Соответствующие unit-тесты теперь живут в
// `internal/apps/kacho/api/address/usecase_test.go` (TestCreateUseCase_* /
// TestUpdateUseCase_* / TestDeleteUseCase_* + TestHandler_FullFlow и т.п.) и
// покрывают тот же набор сценариев.
//
// `makeSubnet` остаётся здесь как shared-helper — он используется в
// `subnet_test.go::TestSubnetService_AddCidrBlocks_Validates` (NetworkID).

func makeSubnet(sr *mockSubnetRepo, networkID string) *domain.Subnet {
	s := &domain.Subnet{
		ID:           ids.NewID(ids.PrefixSubnet),
		FolderID:     "f1",
		NetworkID:    networkID,
		Name:         domain.RcNameVPC("test-subnet"),
		V4CidrBlocks: []string{"10.0.0.0/24"},
	}
	_, _ = sr.Insert(context.Background(), s)
	return s
}
