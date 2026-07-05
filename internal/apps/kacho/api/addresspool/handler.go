// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package addresspool

import (
	"context"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Handler — реализация vpcv1.InternalAddressPoolServiceServer поверх use-case'ов.
// Тонкий transport-слой: proto-request → use-case → proto-response. Никакой
// бизнес-логики.
//
// AddressPool — admin-only ресурс: handler регистрируется только на
// internal-listener 9091 и проброшен через api-gateway internal mux. На external
// TLS endpoint (`api.kacho.local:443`) недоступен.
type Handler struct {
	vpcv1.UnimplementedInternalAddressPoolServiceServer

	create        *CreateAddressPoolUseCase
	update        *UpdateAddressPoolUseCase
	deleteUC      *DeleteAddressPoolUseCase
	get           *GetAddressPoolUseCase
	list          *ListAddressPoolsUseCase
	bindNet       *BindAsNetworkDefaultUseCase
	unbindNet     *UnbindNetworkDefaultUseCase
	utilization   *GetPoolUtilizationUseCase
	listAddresses *ListPoolAddressesUseCase
	addCidr       *AddCidrBlocksUseCase
	removeCidr    *RemoveCidrBlocksUseCase
}

// NewHandler собирает Handler из готовых use-case'ов. Composition root
// (cmd/vpc/main.go) собирает их с одинаковыми зависимостями (repo'ы / clients).
func NewHandler(
	create *CreateAddressPoolUseCase,
	update *UpdateAddressPoolUseCase,
	deleteUC *DeleteAddressPoolUseCase,
	get *GetAddressPoolUseCase,
	list *ListAddressPoolsUseCase,
	bindNet *BindAsNetworkDefaultUseCase,
	unbindNet *UnbindNetworkDefaultUseCase,
	utilization *GetPoolUtilizationUseCase,
	listAddresses *ListPoolAddressesUseCase,
	addCidr *AddCidrBlocksUseCase,
	removeCidr *RemoveCidrBlocksUseCase,
) *Handler {
	return &Handler{
		create:        create,
		update:        update,
		deleteUC:      deleteUC,
		get:           get,
		list:          list,
		bindNet:       bindNet,
		unbindNet:     unbindNet,
		utilization:   utilization,
		listAddresses: listAddresses,
		addCidr:       addCidr,
		removeCidr:    removeCidr,
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
		Kind:             domain.AddressPoolKind(req.GetKind()), // #nosec G115 -- proto enum value (bounded set), not an arithmetic overflow.
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
		Kind:   domain.AddressPoolKind(req.GetKind()), // #nosec G115 -- proto enum value (bounded set), not an arithmetic overflow.
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
	// Единая update_mask-дисциплина (как у всех ресурсов VPC). Тонкий transport:
	// читаем paths из update_mask, нормализуем camelCase→snake_case (REST шлет
	// camelCase, gRPC — snake_case; нормализация идемпотентна), пробрасываем в
	// use-case. Применение/валидацию mask (immutable/unknown → InvalidArgument,
	// пустой mask → full-PATCH) держит use-case.
	in := UpdatePoolReq{
		ID:               req.GetPoolId(),
		UpdateMask:       normalizeMaskPaths(req.GetUpdateMask().GetPaths()),
		Name:             req.GetName(),
		Description:      req.GetDescription(),
		Labels:           req.GetLabels(),
		IsDefault:        req.GetIsDefault(),
		SelectorLabels:   req.GetSelectorLabels(),
		SelectorPriority: req.GetSelectorPriority(),
	}
	rec, err := h.update.Execute(ctx, in)
	if err != nil {
		return nil, mapPoolErr(err)
	}
	return poolToProto(rec), nil
}

// normalizeMaskPaths приводит каждый путь update_mask к snake_case (canonical
// proto field name). REST/protojson сериализует FieldMask camelCase'ом
// (`isDefault`), gRPC-клиент — snake_case'ом (`is_default`); use-case работает
// в snake_case, поэтому нормализуем здесь. camelToSnake идемпотентна на
// уже-snake входе.
func normalizeMaskPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, camelToSnake(p))
	}
	return out
}

// camelToSnake — lowerCamelCase → snake_case (`isDefault`→`is_default`,
// `bogusField`→`bogus_field`). На входе без uppercase возвращает строку без
// изменений (`is_default`→`is_default`, `name`→`name`).
func camelToSnake(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (h *Handler) Delete(ctx context.Context, req *vpcv1.DeleteAddressPoolRequest) (*vpcv1.DeleteAddressPoolResponse, error) {
	if err := h.deleteUC.Execute(ctx, req.GetPoolId()); err != nil {
		return nil, mapPoolErr(err)
	}
	return &vpcv1.DeleteAddressPoolResponse{}, nil
}

// -- CIDR management --

// AddCidrBlocks добавляет CIDR-блоки к пулу (sync). Валидирует family/host-bits,
// дедуплицирует, материализует freelist только для новой v4-дельты. Возвращает
// обновленный AddressPool.
func (h *Handler) AddCidrBlocks(ctx context.Context, req *vpcv1.AddAddressPoolCidrBlocksRequest) (*vpcv1.AddressPool, error) {
	p, err := h.addCidr.Execute(ctx, req.GetAddressPoolId(), req.GetV4CidrBlocks(), req.GetV6CidrBlocks())
	if err != nil {
		return nil, mapPoolErr(err)
	}
	return poolToProto(p), nil
}

// RemoveCidrBlocks удаляет CIDR-блоки из пула (sync). Отвергает удаление CIDR с
// выделенными external-IP (FailedPrecondition) и опустошение пула
// (InvalidArgument). Удаляет соответствующие free_ips. Возвращает обновленный
// AddressPool.
func (h *Handler) RemoveCidrBlocks(ctx context.Context, req *vpcv1.RemoveAddressPoolCidrBlocksRequest) (*vpcv1.AddressPool, error) {
	p, err := h.removeCidr.Execute(ctx, req.GetAddressPoolId(), req.GetV4CidrBlocks(), req.GetV6CidrBlocks())
	if err != nil {
		return nil, mapPoolErr(err)
	}
	return poolToProto(p), nil
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
			ProjectId: a.ProjectID,
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

// poolToProto — AddressPoolRecord → *vpcv1.AddressPool. Локальный inline-помощник
// (а не через `dto.Transfer`): AddressPool — admin-only ресурс, единственный
// consumer этой проекции — handler ниже, поэтому в DTO-реестре он пока не нужен.
// CreatedAt берется из record (DB-managed timestamp); name/labels из
// self-validating newtypes конвертируются в proto-представление.
func poolToProto(rec *kachorepo.AddressPoolRecord) *vpcv1.AddressPool {
	if rec == nil {
		return nil
	}
	return &vpcv1.AddressPool{
		Id:               rec.ID,
		CreatedAt:        timestamppb.New(rec.CreatedAt),
		Name:             string(rec.Name),
		Description:      string(rec.Description),
		Labels:           domain.LabelsToMap(rec.Labels),
		V4CidrBlocks:     rec.V4CIDRBlocks,
		V6CidrBlocks:     rec.V6CIDRBlocks,
		Kind:             vpcv1.AddressPoolKind(rec.Kind),
		ZoneId:           rec.ZoneID,
		IsDefault:        rec.IsDefault,
		SelectorLabels:   domain.LabelsToMap(rec.SelectorLabels),
		SelectorPriority: rec.SelectorPriority,
	}
}

// mapPoolErr — error mapping admin-handler'а.
//
// Тонкая обёртка над единым leak-safe classifier'ом serviceerr.MapRepoErrLeakSafe:
// гарантирует, что raw pgx-text (хранит hostname/db/query-fragment) не уходит в
// response даже на cluster-internal listener'е :9091 (admin-tooling, port-forward,
// lateral movement из соседнего pod). Sentinel service-errors классифицируются
// (голый sentinel.Error()); raw pgErr → generic Internal с fallback-тегом без
// leak'а; уже-сформированный gRPC status (UC-level InvalidArg) идёт как есть.
//
// Классификационный switch раньше жил здесь копией serviceerr.MapRepoErr /
// handler.internalMapErr — консолидирован в один classifier.
func mapPoolErr(err error) error {
	return serviceerr.MapRepoErrLeakSafe(err, "address pool admin error")
}
