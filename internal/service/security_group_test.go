package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// ---------- Mock SecurityGroupRepo ----------

type MockSecurityGroupRepo struct{ mock.Mock }

func (m *MockSecurityGroupRepo) GetByUID(ctx context.Context, uid string) (*domain.SecurityGroup, error) {
	args := m.Called(ctx, uid)
	if v := args.Get(0); v != nil {
		return v.(*domain.SecurityGroup), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockSecurityGroupRepo) GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.SecurityGroup, error) {
	args := m.Called(ctx, folderID, name)
	if v := args.Get(0); v != nil {
		return v.(*domain.SecurityGroup), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockSecurityGroupRepo) List(ctx context.Context, selectors []service.Selector, page service.Pagination) ([]*domain.SecurityGroup, string, int64, error) {
	args := m.Called(ctx, selectors, page)
	return args.Get(0).([]*domain.SecurityGroup), args.String(1), args.Get(2).(int64), args.Error(3)
}
func (m *MockSecurityGroupRepo) SnapshotResourceVersion(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}
func (m *MockSecurityGroupRepo) Insert(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error) {
	args := m.Called(ctx, sg)
	if v := args.Get(0); v != nil {
		return v.(*domain.SecurityGroup), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockSecurityGroupRepo) Update(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error) {
	args := m.Called(ctx, sg)
	if v := args.Get(0); v != nil {
		return v.(*domain.SecurityGroup), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockSecurityGroupRepo) HardDelete(ctx context.Context, uid string) error {
	args := m.Called(ctx, uid)
	return args.Error(0)
}
func (m *MockSecurityGroupRepo) HasDependents(ctx context.Context, uid string) (bool, error) {
	args := m.Called(ctx, uid)
	return args.Bool(0), args.Error(1)
}

// ---------- Tests ----------

// D3: Upsert SG: правила получают server-assigned UUIDs.
func TestSGService_Upsert_RulesGetServerUUIDs(t *testing.T) {
	sgRepo := new(MockSecurityGroupRepo)
	netRepo := new(MockNetworkRepo)
	fc := new(MockFolderClient)
	svc := service.NewSecurityGroupService(sgRepo, netRepo, fc)
	ctx := context.Background()

	folderID := "00000000-0000-0000-0000-000000000030"
	netID := "00000000-0000-0000-0000-000000000031"

	netRepo.On("GetByUID", ctx, netID).Return(&domain.Network{UID: netID, CloudID: "c", OrganizationID: "o"}, nil)
	fc.On("Exists", ctx, folderID).Return(true, nil)
	sgRepo.On("GetByFolderAndName", ctx, folderID, "sg1").Return((*domain.SecurityGroup)(nil), nil)

	var capturedSG *domain.SecurityGroup
	sgRepo.On("Insert", ctx, mock.AnythingOfType("*domain.SecurityGroup")).Run(func(args mock.Arguments) {
		capturedSG = args.Get(1).(*domain.SecurityGroup)
	}).Return(&domain.SecurityGroup{
		UID:     "sg-uid",
		Name:    "sg1",
		FolderID: folderID,
		NetworkID: netID,
		Rules: []domain.SecurityGroupRule{
			{ID: "rule-uid-1", Direction: "INGRESS", Protocol: "TCP"},
		},
		State: "ACTIVE",
	}, nil)

	_, err := svc.Upsert(ctx, &domain.SecurityGroup{
		Name:      "sg1",
		FolderID:  folderID,
		NetworkID: netID,
		Rules: []domain.SecurityGroupRule{
			{Direction: "INGRESS", Protocol: "TCP"}, // ID не задан клиентом
		},
	})

	require.NoError(t, err)
	require.NotNil(t, capturedSG)
	// Сервис должен был назначить UID правилам
	require.Len(t, capturedSG.Rules, 1)
	assert.NotEmpty(t, capturedSG.Rules[0].ID, "сервер должен назначить ID правилу")
}

// Full-replace: после upsert правила полностью заменяются.
func TestSGService_Upsert_FullReplaceRules(t *testing.T) {
	sgRepo := new(MockSecurityGroupRepo)
	netRepo := new(MockNetworkRepo)
	fc := new(MockFolderClient)
	svc := service.NewSecurityGroupService(sgRepo, netRepo, fc)
	ctx := context.Background()

	folderID := "00000000-0000-0000-0000-000000000030"
	netID := "00000000-0000-0000-0000-000000000031"
	sgUID := "00000000-0000-0000-0000-000000000032"

	netRepo.On("GetByUID", ctx, netID).Return(&domain.Network{UID: netID, CloudID: "c", OrganizationID: "o"}, nil)
	fc.On("Exists", ctx, folderID).Return(true, nil)
	sgRepo.On("GetByFolderAndName", ctx, folderID, "sg1").Return(&domain.SecurityGroup{
		UID:      sgUID,
		Name:     "sg1",
		FolderID: folderID,
		NetworkID: netID,
		Rules: []domain.SecurityGroupRule{
			{ID: "old-rule-1", Direction: "INGRESS", Protocol: "TCP"},
			{ID: "old-rule-2", Direction: "EGRESS", Protocol: "ANY"},
		},
	}, nil)

	var capturedSG *domain.SecurityGroup
	sgRepo.On("Update", ctx, mock.AnythingOfType("*domain.SecurityGroup")).Run(func(args mock.Arguments) {
		capturedSG = args.Get(1).(*domain.SecurityGroup)
	}).Return(&domain.SecurityGroup{UID: sgUID, Name: "sg1", Rules: []domain.SecurityGroupRule{
		{ID: "new-rule-1", Direction: "INGRESS", Protocol: "UDP"},
	}}, nil)

	_, err := svc.Upsert(ctx, &domain.SecurityGroup{
		Name:      "sg1",
		FolderID:  folderID,
		NetworkID: netID,
		Rules: []domain.SecurityGroupRule{
			{Direction: "INGRESS", Protocol: "UDP"}, // только одно новое правило
		},
	})

	require.NoError(t, err)
	require.NotNil(t, capturedSG)
	assert.Len(t, capturedSG.Rules, 1, "должно быть только одно новое правило (full-replace)")
	assert.Equal(t, "UDP", capturedSG.Rules[0].Protocol)
	// Новый ID должен быть назначен
	assert.NotEmpty(t, capturedSG.Rules[0].ID)
	// Старые IDs не должны сохраниться
	assert.NotEqual(t, "old-rule-1", capturedSG.Rules[0].ID)
}
