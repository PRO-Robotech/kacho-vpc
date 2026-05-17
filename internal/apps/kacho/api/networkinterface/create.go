package networkinterface

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/macutil"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// niReferrerType — ReferrerType в address_references для адресов, привязанных к NIC.
const niReferrerType = "network_interface"

// niUsedByReferrerType — тип референта в NIC.used_by, когда NIC приаттачен к
// compute-инстансу (зеркало Address.used_by referrer type для NIC/NAT-адресов).
const niUsedByReferrerType = "compute_instance"

// niMacRetryAttempts — количество попыток сгенерировать уникальный MAC при
// cloud-wide UNIQUE-collision (~1e-3 на 1M NIC при 40 битах энтропии — см.
// `internal/apps/kacho/shared/macutil`).
const niMacRetryAttempts = 3

// CreateInput — параметры для CreateNetworkInterfaceUseCase.Execute.
//
// Orthogonal: composes `domain.NetworkInterface` + extra request-only context
// (`InstanceID` / `Index` для immediate-attach к Compute.Instance). Это **не**
// тривиальная обёртка `{NetworkInterface: …}` — InstanceID/Index — самостоятельные
// поля запроса (immediate-attach mode), не атрибуты ресурса. Skill evgeniy §2 B.3 /
// §7 I.1 разрешают композицию domain.X + extra fields; запрет именно на
// `struct{X domain.X}` без других полей (см. KAC-94: тривиальные обёртки удалены
// в Network/Subnet/Gateway/RouteTable/SecurityGroup/PrivateEndpoint).
//
// Поле `n.ID` на входе пустое — назначим внутри use-case'а через
// `ids.NewID(ids.PrefixSubnet)` (NIC переиспользует Subnet-prefix `e9b`).
type CreateInput struct {
	NetworkInterface domain.NetworkInterface
	// InstanceID — опц. сразу приаттачить NIC к инстансу после создания.
	InstanceID string
	// Index — информационный (на какой слот инстанса вешать NIC); не персистим.
	Index string
}

// CreateNetworkInterfaceUseCase инициирует создание NIC. Sync-проверки (folder
// exists, name unique, cardinality v4/v6, address-refs) выполняются ДО создания
// Operation. Async-часть (`doCreate`) — атомарный backstop через FK / DB CHECK
// (миграция 0018, KAC-55) / UNIQUE MAC.
//
// Wave 5 replicate (KAC-94, NIC batch, skill evgeniy §6 G.5 / §7 I.9 / I.10):
// worker открывает ОДНУ writer-TX и делает в ней Insert(NIC) + outbox-emit
// атомарно. Address-attach (SetReference на addresses) пока через legacy
// AddressRepo — отдельная TX (Address ещё не полностью на CQRS-writer для
// SetReference); переход на single writer-TX — следующий шаг replicate-фазы.
//
// A.7 sub-PR 3/6 (KAC-94): parent-Subnet validation в `doCreate` идёт через
// `kachoRepo.Reader().Subnets().Get` (Reader-TX на slave-pool по G.4), а не
// через legacy `*repo.SubnetRepo` peer-port — последняя legacy-зависимость из
// NIC use-case'ов выпилена.
type CreateNetworkInterfaceUseCase struct {
	repo         Repo
	addressRepo  AddressRepo
	projectClient ProjectClient
	opsRepo      operations.Repo
}

// NewCreateNetworkInterfaceUseCase создаёт CreateNetworkInterfaceUseCase.
func NewCreateNetworkInterfaceUseCase(r Repo, addressRepo AddressRepo, projectClient ProjectClient, opsRepo operations.Repo) *CreateNetworkInterfaceUseCase {
	return &CreateNetworkInterfaceUseCase{
		repo:         r,
		addressRepo:  addressRepo,
		projectClient: projectClient,
		opsRepo:      opsRepo,
	}
}

// Execute — sync-валидация + create Operation + запуск worker'а.
func (u *CreateNetworkInterfaceUseCase) Execute(ctx context.Context, in CreateInput) (*operations.Operation, error) {
	n := in.NetworkInterface
	if n.ProjectID == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id required")
	}
	if n.SubnetID == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	// Domain-self-validation (skill evgeniy §4 D.5 / AP-1): Name/Description/Labels
	// + MAC + cardinality v4/v6 (KAC-55) через newtype Validate(). Service-слой
	// больше НЕ зовёт corevalidate.* для этих инвариантов.
	if err := n.Validate(); err != nil {
		return nil, err
	}
	// validateNICAddressCardinality — fast-fail с понятным `invalidArg` (с
	// BadRequest-details для verbatim YC parity); domain.Validate тоже это
	// проверяет, но даёт generic error. См. helpers.go.
	if err := validateNICAddressCardinality(n.V4AddressIDs, n.V6AddressIDs); err != nil {
		return nil, err
	}
	// Sync folder.Exists precheck удалён (KAC-94, skill evgeniy I.4 / AP-5) —
	// race-prone: между sync-проверкой и async-частью folder может быть удалён
	// peer-сервисом, и second-writer-wins безусловно создавал ресурс. Verbatim-YC
	// NotFound теперь возвращается через `operation.error` из async `doCreate`.

	niID := ids.NewID(ids.PrefixSubnet)
	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Create network interface %s", string(n.Name)),
		&vpcv1.CreateNetworkInterfaceMetadata{NetworkInterfaceId: niID},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doCreate(ctx, niID, in)
	})
	return &op, nil
}

// doCreate — async-часть Create (внутри Operation worker'а). Атомарный backstop:
// folder-exists + Subnet.Get + Address-refs валидация + маркировка (used + referrer)
// + Insert NIC. MAC-allocation retry на cloud-wide UNIQUE-collision.
//
// Wave 5 replicate (KAC-94, NIC batch): Insert(NIC) + outbox-emit идут в одной
// writer-TX (skill evgeniy §6 G.5). Address-маркировка — пока legacy (отдельная
// TX в `AddressRepo.SetReference`); rollback маркировки при ошибке Insert —
// best-effort через `detachAddresses`.
func (u *CreateNetworkInterfaceUseCase) doCreate(ctx context.Context, niID string, in CreateInput) (*anypb.Any, error) {
	n := in.NetworkInterface
	exists, err := u.projectClient.Exists(ctx, n.ProjectID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", n.ProjectID)
	}
	// A.7 sub-PR 3/6 (KAC-94): parent-Subnet check через CQRS-Reader (G.4 — на
	// slave-pool, если он настроен). DB-уровень backstop остаётся: FK
	// `network_interfaces.subnet_id → subnets.id` ON DELETE RESTRICT — если
	// между sync-Get и Insert'ом подсеть удалят, Insert провалится с
	// foreign_key_violation → `mapRepoErr` → FailedPrecondition.
	rd, rerr := u.repo.Reader(ctx)
	if rerr != nil {
		return nil, mapRepoErr(rerr)
	}
	_, serr := rd.Subnets().Get(ctx, n.SubnetID)
	_ = rd.Close()
	if serr != nil {
		return nil, mapRepoErr(serr)
	}
	// Валидируем ссылки на Address-ресурсы (существуют, нужной версии, в той же
	// подсети, не заняты другим референтом) и помечаем их used=true + referrer.
	// Best-effort v1: валидация/маркировка адресов и Insert NIC не в одной tx —
	// rollback маркировки при ошибке делается через `detachAddresses` ниже.
	if err := u.validateAndAttachAddresses(ctx, niID, string(n.Name), n.SubnetID, n.V4AddressIDs, n.V6AddressIDs); err != nil {
		return nil, err
	}
	st := domain.NIStatusAvailable
	usedByType, usedByID := "", ""
	if in.InstanceID != "" {
		st = domain.NIStatusActive
		usedByType, usedByID = niUsedByReferrerType, in.InstanceID
	}
	rec := &domain.NetworkInterface{
		ID:               niID,
		ProjectID:         n.ProjectID,
		Name:             n.Name,
		Description:      n.Description,
		Labels:           n.Labels,
		SubnetID:         n.SubnetID,
		V4AddressIDs:     n.V4AddressIDs,
		V6AddressIDs:     n.V6AddressIDs,
		SecurityGroupIDs: n.SecurityGroupIDs,
		UsedByType:       usedByType,
		UsedByID:         usedByID,
		Status:           st,
	}
	allAddrs := append(append([]string{}, n.V4AddressIDs...), n.V6AddressIDs...)
	// MAC аллоцируется здесь и больше не меняется на жизни NIC (AWS-ENI semantics).
	// При cloud-wide UNIQUE-collision генерируем новый MAC и повторяем Insert.
	// Каждая попытка — отдельная writer-TX (CAS-конфликт на MAC требует start-over).
	for attempt := 0; attempt < niMacRetryAttempts; attempt++ {
		mac, merr := macutil.GenerateMAC()
		if merr != nil {
			u.detachAddresses(ctx, allAddrs)
			return nil, status.Errorf(codes.Internal, "generate mac: %v", merr)
		}
		rec.MAC = mac

		w, werr := u.repo.Writer(ctx)
		if werr != nil {
			u.detachAddresses(ctx, allAddrs)
			return nil, mapRepoErr(werr)
		}
		created, insertErr := w.NetworkInterfaces().Insert(ctx, rec)
		if insertErr != nil {
			w.Abort()
			if errors.Is(insertErr, repo.ErrMacCollision) {
				continue // retry с новым MAC
			}
			u.detachAddresses(ctx, allAddrs)
			return nil, mapRepoErr(insertErr)
		}
		if oerr := w.Outbox().Emit(ctx, "NetworkInterface", created.ID, "CREATED", networkInterfacePayloadMap(created)); oerr != nil {
			w.Abort()
			u.detachAddresses(ctx, allAddrs)
			return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
		}
		if cerr := w.Commit(); cerr != nil {
			u.detachAddresses(ctx, allAddrs)
			return nil, mapRepoErr(cerr)
		}
		return marshalNetworkInterfaceRecord(created)
	}
	// Все попытки исчерпаны — rollback маркировки адресов best-effort.
	u.detachAddresses(ctx, allAddrs)
	return nil, status.Errorf(codes.Internal, "could not allocate unique MAC after %d attempts", niMacRetryAttempts)
}

// validateAddressRef проверяет, что Address id существует, имеет ожидаемую
// IP-версию, (для internal) лежит в подсети nicSubnet и не занят. Возвращает
// gRPC-status при нарушении.
func (u *CreateNetworkInterfaceUseCase) validateAddressRef(ctx context.Context, id, nicSubnet string, want domain.IpVersion) error {
	a, err := u.addressRepo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return status.Errorf(codes.InvalidArgument, "address %s not found", id)
		}
		return mapRepoErr(err)
	}
	switch want {
	case domain.IpVersionIPv4:
		if a.Type != domain.AddressTypeInternal || a.InternalIpv4 == nil {
			return status.Errorf(codes.InvalidArgument, "address %s is not an internal IPv4 address", id)
		}
		if a.InternalIpv4.SubnetID != nicSubnet {
			return status.Errorf(codes.InvalidArgument, "address %s belongs to subnet %s, not %s", id, a.InternalIpv4.SubnetID, nicSubnet)
		}
	case domain.IpVersionIPv6:
		if a.IpVersion != domain.IpVersionIPv6 || a.InternalIpv6 == nil {
			return status.Errorf(codes.InvalidArgument, "address %s is not an internal IPv6 address", id)
		}
		if a.InternalIpv6.SubnetID != nicSubnet {
			return status.Errorf(codes.InvalidArgument, "address %s belongs to subnet %s, not %s", id, a.InternalIpv6.SubnetID, nicSubnet)
		}
	}
	if a.Used {
		return status.Errorf(codes.FailedPrecondition, "address %s is already in use", id)
	}
	return nil
}

// validateAndAttachAddresses валидирует все v4/v6 address-refs, затем помечает
// каждый used=true + referrer={network_interface, nicID, nicName}. Best-effort:
// если что-то падает в середине, ранее размеченные адреса откатываются.
//
// **Address attach-race (workspace CLAUDE.md §«Within-service refs — DB-уровень
// обязателен», запрет #10):** SetReference на repo-уровне делает atomic CAS на
// `addresses.used` — параллельная попытка занять тот же адрес → ErrFailedPrecondition.
// Этот use-case ловит ошибку, откатывает уже-attached и возвращает её клиенту.
func (u *CreateNetworkInterfaceUseCase) validateAndAttachAddresses(ctx context.Context, nicID, nicName, nicSubnet string, v4IDs, v6IDs []string) error {
	for _, id := range v4IDs {
		if err := u.validateAddressRef(ctx, id, nicSubnet, domain.IpVersionIPv4); err != nil {
			return err
		}
	}
	for _, id := range v6IDs {
		if err := u.validateAddressRef(ctx, id, nicSubnet, domain.IpVersionIPv6); err != nil {
			return err
		}
	}
	var attached []string
	for _, id := range append(append([]string{}, v4IDs...), v6IDs...) {
		ref := &domain.AddressReference{AddressID: id, ReferrerType: niReferrerType, ReferrerID: nicID, ReferrerName: nicName}
		if _, err := u.addressRepo.SetReference(ctx, ref); err != nil {
			u.detachAddresses(ctx, attached)
			return mapRepoErr(err)
		}
		attached = append(attached, id)
	}
	return nil
}

// detachAddresses снимает used + referrer-row с каждого address id (best-effort,
// ошибки логируются неявно — пропускаются).
func (u *CreateNetworkInterfaceUseCase) detachAddresses(ctx context.Context, ids []string) {
	for _, id := range ids {
		_ = u.addressRepo.ClearReference(ctx, id)
	}
}
