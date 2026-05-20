package subnet

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"log/slog"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgawrite"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// CreateSubnetUseCase инициирует создание Subnet. Sync-проверки (folder exists,
// parent network exists, name unique, CIDR validity / non-overlap) выполняются
// ДО создания Operation — клиент получает fast-fail gRPC-status, а не «200 +
// операция, упавшая через секунду» (см. kacho-vpc#8). Async-часть (`doCreate`)
// — атомарный backstop через FK + EXCLUDE constraint.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.5): worker открывает ОДНУ
// Writer-TX и делает Insert(Subnet) + outbox-emit Subnet.CREATED атомарно.
type CreateSubnetUseCase struct {
	repo          Repo
	projectClient ProjectClient
	zoneReg       ZoneRegistry
	opsRepo       operations.Repo

	// fgaWriter / logger — KAC-127 issue #22: publish
	// `vpc_subnet:<id>#project@project:<project_id>` after commit. nil → no-op.
	fgaWriter fgawrite.HierarchyTupleWriter
	logger    *slog.Logger
}

// WithFGAWriter wires the OpenFGA hierarchy-tuple writer (KAC-127 issue #22).
func (u *CreateSubnetUseCase) WithFGAWriter(w fgawrite.HierarchyTupleWriter, logger *slog.Logger) *CreateSubnetUseCase {
	u.fgaWriter = w
	u.logger = logger
	return u
}

// NewCreateSubnetUseCase создаёт CreateSubnetUseCase.
func NewCreateSubnetUseCase(
	r Repo,
	projectClient ProjectClient,
	zoneReg ZoneRegistry,
	opsRepo operations.Repo,
) *CreateSubnetUseCase {
	return &CreateSubnetUseCase{
		repo:          r,
		projectClient: projectClient,
		zoneReg:       zoneReg,
		opsRepo:       opsRepo,
	}
}

// Execute — sync-валидация + create Operation + запуск worker'а.
//
// Принимает `domain.Subnet` напрямую (KAC-94, skill evgeniy §2 B.3 / §7 I.1):
// тривиальная `CreateInput{Subnet: …}`-обёртка удалена — она лишь
// перепаковывала domain.X без дополнительного контекста. Поле `s.ID` на входе
// пустое — назначим внутри use-case'а через `ids.NewID(ids.PrefixSubnet)`.
func (u *CreateSubnetUseCase) Execute(ctx context.Context, s domain.Subnet) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, s.NetworkID); err != nil {
		return nil, err
	}
	if s.ProjectID == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	if s.NetworkID == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	// ZoneId: required + existence в таблице `zones` (без hardcoded whitelist).
	if err := validateZoneID(ctx, u.zoneReg, "zone_id", s.ZoneID); err != nil {
		return nil, err
	}
	// Proto contract: v4_cidr_blocks больше НЕ required — подсеть может быть
	// создана без IPv4-диапазона (kacho-proto#8). Пустой список — легален; CIDR'ы,
	// которые ПЕРЕДАНЫ, всё ещё валидируются (host-bits=0, /16../28).
	for i, c := range s.V4CidrBlocks {
		if err := validateSubnetV4CIDR(fmt.Sprintf("v4_cidr_blocks[%d]", i), c); err != nil {
			return nil, err
		}
	}
	// v6_cidr_blocks — опциональны; если переданы, валидируем как IPv6 CIDR
	// (host-bits=0). Immutable после Create (как v4).
	for i, c := range s.V6CidrBlocks {
		if err := validateSubnetV6CIDR(fmt.Sprintf("v6_cidr_blocks[%d]", i), c); err != nil {
			return nil, err
		}
	}
	// Domain-self-validation (skill evgeniy §4 D.5 / AP-1): Name/Description/Labels
	// валидируются через newtypes — use-case-слой больше НЕ зовёт corevalidate.NameVPC/
	// Description/Labels.
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if err := validateDhcpOptions(s.DhcpOptions); err != nil {
		return nil, err
	}

	// Sync folder.Exists precheck удалён (KAC-94, skill evgeniy I.4 / AP-5) —
	// race-prone: между sync-проверкой и async-частью folder может быть удалён
	// peer-сервисом, и second-writer-wins безусловно создавал ресурс. Verbatim-YC
	// NotFound теперь возвращается через `operation.error` из async `doCreate`.
	// Sync uniqueness/overlap-проверки (через DB-state в той же сервис-БД)
	// остаются — они race-free относительно peer-сервисов.
	//
	// Sync existence / uniqueness / overlap — все через single Reader-TX.
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if _, gerr := rd.Networks().Get(ctx, s.NetworkID); gerr != nil {
		_ = rd.Close()
		if errors.Is(gerr, repo.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "Network %s not found", s.NetworkID)
		}
		return nil, mapRepoErr(gerr)
	}
	name := string(s.Name)
	if name != "" {
		existing, _, lerr := rd.Subnets().List(ctx, SubnetFilter{ProjectID: s.ProjectID, Name: name}, Pagination{})
		if lerr != nil {
			_ = rd.Close()
			return nil, mapRepoErr(lerr)
		}
		if len(existing) > 0 {
			_ = rd.Close()
			return nil, status.Errorf(codes.AlreadyExists, "Subnet with name %s already exists", name)
		}
	}
	if err := u.checkSubnetCIDROverlap(ctx, rd, s.ProjectID, s.NetworkID, s.V4CidrBlocks); err != nil {
		_ = rd.Close()
		return nil, err
	}
	_ = rd.Close()

	subID := ids.NewID(ids.PrefixSubnet)
	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Create subnet %s", name),
		&vpcv1.CreateSubnetMetadata{SubnetId: subID},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doCreate(ctx, subID, s)
	})

	return &op, nil
}

// doCreate — async-часть Create (внутри Operation worker'а). Атомарный backstop:
// folder-exists + parent network-exists + Insert (FK ограничения / EXCLUDE для
// overlap) + outbox-emit Subnet.CREATED — всё в одной writer-TX.
func (u *CreateSubnetUseCase) doCreate(ctx context.Context, subID string, s domain.Subnet) (*anypb.Any, error) {
	exists, err := u.projectClient.Exists(ctx, s.ProjectID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", s.ProjectID)
	}

	s.ID = subID

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()

	// Parent network existence — повторная проверка в writer-TX (atomic backstop
	// — FK violation на subnets.network_id даст 23503; sync-check уже отверг бы).
	if _, gerr := w.Networks().Get(ctx, s.NetworkID); gerr != nil {
		return nil, status.Errorf(codes.NotFound, "Network %s not found", s.NetworkID)
	}

	// SU-CIDR-OVERLAP — пересечения v4 CIDR в рамках одной VPC ловятся атомарно
	// DB-level EXCLUDE constraint (миграция 0007), pg-impl маппит SQLSTATE 23P01
	// на ErrFailedPrecondition.
	created, err := w.Subnets().Insert(ctx, &s)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "Subnet", created.ID, "CREATED", subnetPayloadMap(created)); err != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	if err := w.Commit(); err != nil {
		return nil, mapRepoErr(err)
	}
	// KAC-127 issue #22: publish vpc_subnet→project hierarchy tuple.
	fgawrite.Emit(ctx, u.fgaWriter, u.logger, "vpc_subnet", created.ID, string(s.ProjectID))
	return marshalSubnetRecord(created)
}

// checkSubnetCIDROverlap — sync FAILED_PRECONDITION "Subnet CIDRs can not
// overlap" if any of the requested v4 CIDRs overlaps a CIDR of an existing
// subnet in the same network/folder. The DB EXCLUDE constraint (миграция 0007)
// stays as the atomic backstop in doCreate. См. kacho-vpc#8.
func (u *CreateSubnetUseCase) checkSubnetCIDROverlap(ctx context.Context, rd Reader, folderID, networkID string, v4 []string) error {
	if len(v4) == 0 {
		return nil
	}
	newPrefixes := make([]netipPrefix, 0, len(v4))
	for _, c := range v4 {
		pr, err := parseNetipPrefix(c)
		if err != nil {
			// host-bits / format already validated upstream; be defensive.
			return invalidArg("v4_cidr_blocks", "must be valid CIDR")
		}
		newPrefixes = append(newPrefixes, pr)
	}
	existing, _, err := rd.Subnets().List(ctx, SubnetFilter{ProjectID: folderID, NetworkID: networkID}, Pagination{})
	if err != nil {
		return mapRepoErr(err)
	}
	for _, sub := range existing {
		for _, raw := range sub.V4CidrBlocks {
			pr, perr := parseNetipPrefix(raw)
			if perr != nil {
				continue
			}
			for _, np := range newPrefixes {
				if prefixesOverlap(pr, np) {
					return status.Errorf(codes.FailedPrecondition, "Subnet CIDRs can not overlap")
				}
			}
		}
	}
	return nil
}
