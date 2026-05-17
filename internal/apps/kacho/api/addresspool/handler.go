package addresspool

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// Handler — реализация vpcv1.InternalAddressPoolServiceServer на основе
// use-case'ов (skill evgeniy §2). Тонкий transport-слой: proto-request →
// use-case → proto-response. Никакой бизнес-логики.
//
// AddressPool — admin-only ресурс (см. workspace CLAUDE.md §«Запреты» #6,
// kacho-vpc CLAUDE.md §16): handler регистрируется только на internal-listener
// 9091 и проброшен через api-gateway internal mux. На external TLS endpoint
// (`api.kacho.local:443`) недоступен.
type Handler struct {
	vpcv1.UnimplementedInternalAddressPoolServiceServer

	create        *CreateAddressPoolUseCase
	update        *UpdateAddressPoolUseCase
	deleteUC      *DeleteAddressPoolUseCase
	get           *GetAddressPoolUseCase
	list          *ListAddressPoolsUseCase
	check         *CheckUseCase
	explain       *ExplainResolutionUseCase
	bindNet       *BindAsNetworkDefaultUseCase
	unbindNet     *UnbindNetworkDefaultUseCase
	bindAddr      *BindAsAddressOverrideUseCase
	unbindAddr    *UnbindAddressOverrideUseCase
	utilization   *GetPoolUtilizationUseCase
	listAddresses *ListPoolAddressesUseCase
}

// NewHandler собирает Handler из готовых use-case'ов. Composition root
// (cmd/vpc/main.go) собирает их с одинаковыми зависимостями (repo'ы / clients).
func NewHandler(
	create *CreateAddressPoolUseCase,
	update *UpdateAddressPoolUseCase,
	deleteUC *DeleteAddressPoolUseCase,
	get *GetAddressPoolUseCase,
	list *ListAddressPoolsUseCase,
	check *CheckUseCase,
	explain *ExplainResolutionUseCase,
	bindNet *BindAsNetworkDefaultUseCase,
	unbindNet *UnbindNetworkDefaultUseCase,
	bindAddr *BindAsAddressOverrideUseCase,
	unbindAddr *UnbindAddressOverrideUseCase,
	utilization *GetPoolUtilizationUseCase,
	listAddresses *ListPoolAddressesUseCase,
) *Handler {
	return &Handler{
		create:        create,
		update:        update,
		deleteUC:      deleteUC,
		get:           get,
		list:          list,
		check:         check,
		explain:       explain,
		bindNet:       bindNet,
		unbindNet:     unbindNet,
		bindAddr:      bindAddr,
		unbindAddr:    unbindAddr,
		utilization:   utilization,
		listAddresses: listAddresses,
	}
}

// -- CRUD --

func (h *Handler) Create(ctx context.Context, req *vpcv1.CreateAddressPoolRequest) (*vpcv1.AddressPool, error) {
	p, err := h.create.Execute(ctx, CreatePoolReq{
		Name:             req.GetName(),
		Description:      req.GetDescription(),
		Labels:           req.GetLabels(),
		V4CIDRBlocks:     req.GetV4CidrBlocks(),
		V6CIDRBlocks:     req.GetV6CidrBlocks(),
		Kind:             domain.AddressPoolKind(req.GetKind()),
		ZoneID:           req.GetZoneId(),
		IsDefault:        req.GetIsDefault(),
		SelectorLabels:   req.GetSelectorLabels(),
		SelectorPriority: req.GetSelectorPriority(),
	})
	if err != nil {
		return nil, mapPoolErr(err)
	}
	return poolToProto(p), nil
}

func (h *Handler) Get(ctx context.Context, req *vpcv1.GetAddressPoolRequest) (*vpcv1.AddressPool, error) {
	p, err := h.get.Execute(ctx, req.GetPoolId())
	if err != nil {
		return nil, mapPoolErr(err)
	}
	return poolToProto(p), nil
}

func (h *Handler) List(ctx context.Context, req *vpcv1.ListAddressPoolsRequest) (*vpcv1.ListAddressPoolsResponse, error) {
	pools, next, err := h.list.Execute(ctx, AddressPoolFilter{
		Kind:   domain.AddressPoolKind(req.GetKind()),
		ZoneID: req.GetZoneId(),
	}, Pagination{
		PageToken: req.GetPageToken(),
		PageSize:  req.GetPageSize(),
	})
	if err != nil {
		return nil, mapPoolErr(err)
	}
	out := make([]*vpcv1.AddressPool, 0, len(pools))
	for _, p := range pools {
		out = append(out, poolToProto(p))
	}
	return &vpcv1.ListAddressPoolsResponse{Pools: out, NextPageToken: next}, nil
}

func (h *Handler) Update(ctx context.Context, req *vpcv1.UpdateAddressPoolRequest) (*vpcv1.AddressPool, error) {
	in := UpdatePoolReq{ID: req.GetPoolId()}
	if req.GetName() != "" {
		n := req.GetName()
		in.Name = &n
	}
	if req.GetDescription() != "" {
		d := req.GetDescription()
		in.Description = &d
	}
	if req.GetReplaceLabels() {
		in.ReplaceLabels = true
		in.Labels = req.GetLabels()
	}
	// KAC-71: replace выполняется ТОЛЬКО при выставленном явном bool-флаге.
	// Тело массива (включая пустой) значимо лишь при флаге true; иначе
	// игнорируется. См. REQ-IPL-UPD-01..06 / B7..B12.
	if req.GetReplaceV4CidrBlocks() {
		in.ReplaceV4CIDR = true
		in.V4CIDRBlocks = req.GetV4CidrBlocks()
	}
	if req.GetReplaceV6CidrBlocks() {
		in.ReplaceV6CIDR = true
		in.V6CIDRBlocks = req.GetV6CidrBlocks()
	}
	if req.GetUpdateIsDefault() {
		in.UpdateIsDefault = true
		in.IsDefault = req.GetIsDefault()
	}
	if req.GetReplaceSelectorLabels() {
		in.ReplaceSelectorLabels = true
		in.SelectorLabels = req.GetSelectorLabels()
	}
	if req.GetUpdateSelectorPriority() {
		in.UpdateSelectorPriority = true
		in.SelectorPriority = req.GetSelectorPriority()
	}
	p, err := h.update.Execute(ctx, in)
	if err != nil {
		return nil, mapPoolErr(err)
	}
	return poolToProto(p), nil
}

func (h *Handler) Delete(ctx context.Context, req *vpcv1.DeleteAddressPoolRequest) (*vpcv1.DeleteAddressPoolResponse, error) {
	if err := h.deleteUC.Execute(ctx, req.GetPoolId()); err != nil {
		return nil, mapPoolErr(err)
	}
	return &vpcv1.DeleteAddressPoolResponse{}, nil
}

// -- Bindings --

func (h *Handler) BindAsNetworkDefault(ctx context.Context, req *vpcv1.BindAsNetworkDefaultRequest) (*vpcv1.BindResponse, error) {
	if err := h.bindNet.Execute(ctx, req.GetNetworkId(), req.GetPoolId()); err != nil {
		return nil, mapPoolErr(err)
	}
	return &vpcv1.BindResponse{}, nil
}

func (h *Handler) UnbindNetworkDefault(ctx context.Context, req *vpcv1.UnbindNetworkDefaultRequest) (*vpcv1.BindResponse, error) {
	if err := h.unbindNet.Execute(ctx, req.GetNetworkId()); err != nil {
		return nil, mapPoolErr(err)
	}
	return &vpcv1.BindResponse{}, nil
}

func (h *Handler) BindAsAddressOverride(ctx context.Context, req *vpcv1.BindAsAddressOverrideRequest) (*vpcv1.BindResponse, error) {
	if err := h.bindAddr.Execute(ctx, req.GetAddressId(), req.GetPoolId()); err != nil {
		return nil, mapPoolErr(err)
	}
	return &vpcv1.BindResponse{}, nil
}

func (h *Handler) UnbindAddressOverride(ctx context.Context, req *vpcv1.UnbindAddressOverrideRequest) (*vpcv1.BindResponse, error) {
	if err := h.unbindAddr.Execute(ctx, req.GetAddressId()); err != nil {
		return nil, mapPoolErr(err)
	}
	return &vpcv1.BindResponse{}, nil
}

// -- Diagnostics --

func (h *Handler) Check(ctx context.Context, req *vpcv1.CheckRequest) (*vpcv1.CheckResponse, error) {
	warnings, err := h.check.Execute(ctx, req.GetZoneId())
	if err != nil {
		return nil, mapPoolErr(err)
	}
	return &vpcv1.CheckResponse{Warnings: warnings}, nil
}

// ExplainResolution — admin diagnostic. REQ-RESOLVE-04 / D4: при fall-through
// (никаких pool требуемой family) возвращаем HTTP 200 с `matched_via="none"`
// и пустым `selected_pool`, а НЕ FailedPrecondition (как делают
// AllocateExternalIPv4 / AllocateExternalIPv6 при той же ошибке). ExplainResolution —
// диагностический endpoint для admin-UI; «нет подходящего pool» — нормальный
// ответ, который должен рендериться без error-handling.
//
// Прочие ошибки (NotFound и т.п.) идут через стандартный mapPoolErr.
func (h *Handler) ExplainResolution(ctx context.Context, req *vpcv1.ExplainResolutionRequest) (*vpcv1.ExplainResolutionResponse, error) {
	primary, runner, err := h.explain.Execute(ctx, req.GetAddressId(), req.GetNetworkId())
	if err != nil {
		if errors.Is(err, ErrPoolNotResolved) {
			return &vpcv1.ExplainResolutionResponse{MatchedVia: "none"}, nil
		}
		return nil, mapPoolErr(err)
	}
	out := &vpcv1.ExplainResolutionResponse{}
	if primary != nil {
		out.SelectedPool = poolToProto(primary.Pool)
		out.MatchedVia = primary.MatchedVia
		out.MatchedSelector = primary.MatchedSelector
	}
	if runner != nil {
		out.RunnerUpPool = poolToProto(runner.Pool)
		out.RunnerUpMatchedVia = runner.MatchedVia
	}
	return out, nil
}

// -- Admin observability --

func (h *Handler) ListAddresses(ctx context.Context, req *vpcv1.ListAddressPoolAddressesRequest) (*vpcv1.ListAddressPoolAddressesResponse, error) {
	addrs, next, err := h.listAddresses.Execute(ctx, req.GetPoolId(), req.GetProjectId(), Pagination{
		PageToken: req.GetPageToken(),
		PageSize:  req.GetPageSize(),
	})
	if err != nil {
		return nil, mapPoolErr(err)
	}
	out := make([]*vpcv1.AddressPoolAddressEntry, 0, len(addrs))
	for _, a := range addrs {
		ip := ""
		zone := ""
		if a.ExternalIpv4 != nil {
			ip = a.ExternalIpv4.Address
			zone = a.ExternalIpv4.ZoneID
		}
		out = append(out, &vpcv1.AddressPoolAddressEntry{
			Id:        a.ID,
			ProjectId:  a.ProjectID,
			Name:      string(a.Name),
			Ipv4:      ip,
			ZoneId:    zone,
			Reserved:  a.Reserved,
			Used:      a.Used,
			CreatedAt: timestamppb.New(a.CreatedAt),
		})
	}
	return &vpcv1.ListAddressPoolAddressesResponse{Addresses: out, NextPageToken: next}, nil
}

func (h *Handler) GetUtilization(ctx context.Context, req *vpcv1.GetAddressPoolUtilizationRequest) (*vpcv1.AddressPoolUtilization, error) {
	u, err := h.utilization.Execute(ctx, req.GetPoolId())
	if err != nil {
		return nil, mapPoolErr(err)
	}
	cidrs := make([]*vpcv1.CIDRUtilization, 0, len(u.CIDRs))
	for _, c := range u.CIDRs {
		cidrs = append(cidrs, &vpcv1.CIDRUtilization{Cidr: c.CIDR, Total: c.Total, Used: c.Used})
	}
	return &vpcv1.AddressPoolUtilization{
		PoolId:      u.PoolID,
		TotalIps:    u.TotalIPs,
		UsedIps:     u.UsedIPs,
		FreeIps:     u.FreeIPs,
		UsedPercent: u.UsedPercent,
		Cidrs:       cidrs,
	}, nil
}

// -- helpers --

// poolToProto — domain.AddressPool → *vpcv1.AddressPool. Локальный inline-помощник
// (а не через `dto.Transfer`) — AddressPool пока не зарегистрирован в DTO-реестре
// (admin-only ресурс, единственный consumer этой проекции — handler ниже).
// Регистрация AP в `dto/toproto/` — отдельная итерация; параллельно с CQRS
// расширением (когда будет AddressPoolRecord).
func poolToProto(p *domain.AddressPool) *vpcv1.AddressPool {
	if p == nil {
		return nil
	}
	return &vpcv1.AddressPool{
		Id:               p.ID,
		CreatedAt:        timestamppb.New(p.CreatedAt),
		Name:             p.Name,
		Description:      p.Description,
		Labels:           p.Labels,
		V4CidrBlocks:     p.V4CIDRBlocks,
		V6CidrBlocks:     p.V6CIDRBlocks,
		Kind:             vpcv1.AddressPoolKind(p.Kind),
		ZoneId:           p.ZoneID,
		IsDefault:        p.IsDefault,
		SelectorLabels:   p.SelectorLabels,
		SelectorPriority: p.SelectorPriority,
	}
}

// mapPoolErr — admin-handler error mapping (parity с legacy `mapPoolErr` →
// `internalMapErr`-flow в `internal/handler/internal_address_pool_handler.go`).
//
// Назначение: гарантировать что raw pgx-text (хранит hostname/db/query
// fragment) не уходит в response даже на cluster-internal listener'е :9091
// (admin-tooling, port-forward, lateral movement из соседнего pod). R8 M1
// closure: до этого админ-handler возвращал err.Error() в Internal branch.
//
// Sentinel service-errors классифицируются; raw pgErr → generic Internal
// без leak'а; уже-сформированный gRPC status (UC-level InvalidArg) идёт как есть.
func mapPoolErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, ErrNotFound.Error())
	case errors.Is(err, serviceerr.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, serviceerr.ErrAlreadyExists.Error())
	case errors.Is(err, serviceerr.ErrFailedPrecondition):
		return status.Error(codes.FailedPrecondition, serviceerr.ErrFailedPrecondition.Error())
	case errors.Is(err, ErrPoolNotResolved):
		// FINDING-008: ни один шаг IPAM cascade не дал pool — это FailedPrecondition
		// (конфигурация пулов неполна), а не INTERNAL. Без leak'а raw-текста.
		return status.Error(codes.FailedPrecondition, ErrPoolNotResolved.Error())
	case errors.Is(err, serviceerr.ErrInvalidArg):
		return status.Error(codes.InvalidArgument, serviceerr.ErrInvalidArg.Error())
	}
	// Уже-сформированный gRPC status (не Unknown) пробрасываем — например
	// status.Error из самого UC-слоя (InvalidArgument из CreateAddressPoolUseCase).
	if st, ok := status.FromError(err); ok && st.Code() != codes.Unknown {
		return err
	}
	// Defensive: raw err — без leak'а текста.
	return status.Error(codes.Internal, "address pool admin error")
}
