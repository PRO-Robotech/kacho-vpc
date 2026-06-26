// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package address

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	operationpb "github.com/PRO-Robotech/kacho-corelib/proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/pbconv"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"

	// Blank-import регистрирует Address/time DTO-трансферы через init().
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/tenant"
)

// SubnetAuthZGetter — узкий port для AuthZ pre-check на ListBySubnet (caller
// обязан владеть parent subnet'ом). Реализуется `*service.SubnetService.Get`.
// В unit-тестах допустим nil (skip AuthZ); production composition root в
// cmd/vpc/main.go обязан передать non-nil.
type SubnetAuthZGetter interface {
	Get(ctx context.Context, id string) (*kachorepo.SubnetRecord, error)
}

// Handler — реализация vpcv1.AddressServiceServer на основе use-case'ов.
// Тонкий transport-слой: proto-request → domain → use-case → proto-response.
// Никакой бизнес-логики.
type Handler struct {
	vpcv1.UnimplementedAddressServiceServer

	create         *CreateAddressUseCase
	update         *UpdateAddressUseCase
	delete         *DeleteAddressUseCase
	get            *GetAddressUseCase
	getByValue     *GetByValueUseCase
	list           *ListAddressesUseCase
	listBySubnet   *ListBySubnetUseCase
	listOperations *ListOperationsUseCase
	subnetAuthZ    SubnetAuthZGetter // optional; AuthZ pre-check для ListBySubnet
}

// NewHandler собирает Handler из готовых use-case'ов. Конструктор намеренно
// принимает все use-case'ы — composition-root (cmd/vpc/main.go) собирает их
// с одинаковыми зависимостями (repo / subnetReader / projectClient / opsRepo /
// pools).
func NewHandler(
	create *CreateAddressUseCase,
	update *UpdateAddressUseCase,
	deleteUC *DeleteAddressUseCase,
	get *GetAddressUseCase,
	getByValue *GetByValueUseCase,
	list *ListAddressesUseCase,
	listBySubnet *ListBySubnetUseCase,
	listOps *ListOperationsUseCase,
	subnetAuthZ SubnetAuthZGetter,
) *Handler {
	return &Handler{
		create:         create,
		update:         update,
		delete:         deleteUC,
		get:            get,
		getByValue:     getByValue,
		list:           list,
		listBySubnet:   listBySubnet,
		listOperations: listOps,
		subnetAuthZ:    subnetAuthZ,
	}
}

// Get — sync read + AuthZ + per-object no-leak.
func (h *Handler) Get(ctx context.Context, req *vpcv1.GetAddressRequest) (*vpcv1.Address, error) {
	if req.AddressId == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	subject := pbconv.SubjectFromContext(ctx)
	a, err := h.get.Execute(ctx, subject, req.AddressId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, a.ProjectID); err != nil {
		return nil, err
	}
	return addressToPb(a)
}

// GetByValue — sync lookup-by-IP + post-fetch AuthZ. Если адрес не принадлежит
// caller'у — маскируем под NotFound (не раскрываем существование IP в чужом
// project'е).
func (h *Handler) GetByValue(ctx context.Context, req *vpcv1.GetAddressByValueRequest) (*vpcv1.Address, error) {
	externalIP := req.GetExternalIpv4Address()
	internalIP := req.GetInternalIpv4Address()
	subnetID := req.GetSubnetId()
	a, err := h.getByValue.Execute(ctx, externalIP, internalIP, subnetID)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, a.ProjectID); err != nil {
		return nil, status.Error(codes.NotFound, "Address not found")
	}
	return addressToPb(a)
}

// ListBySubnet — child list; caller обязан владеть parent subnet'ом (если
// subnetAuthZ инжектирован).
func (h *Handler) ListBySubnet(ctx context.Context, req *vpcv1.ListAddressesBySubnetRequest) (*vpcv1.ListAddressesBySubnetResponse, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	if h.subnetAuthZ != nil {
		sub, err := h.subnetAuthZ.Get(ctx, req.SubnetId)
		if err != nil {
			return nil, err
		}
		if err := tenant.AssertProjectOwnership(ctx, sub.ProjectID); err != nil {
			return nil, err
		}
	}
	addrs, nextToken, err := h.listBySubnet.Execute(ctx, req.SubnetId, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListAddressesBySubnetResponse{NextPageToken: nextToken}
	for _, a := range addrs {
		pb, err := addressToPb(a)
		if err != nil {
			return nil, err
		}
		resp.Addresses = append(resp.Addresses, pb)
	}
	return resp, nil
}

// List — project_id required + AuthZ + FGA list-filter.
func (h *Handler) List(ctx context.Context, req *vpcv1.ListAddressesRequest) (*vpcv1.ListAddressesResponse, error) {
	if err := tenant.AssertProjectOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	subject := pbconv.SubjectFromContext(ctx)
	addrs, nextToken, err := h.list.Execute(ctx, subject, AddressFilter{
		ProjectID: req.ProjectId,
		Filter:    req.Filter,
		SubnetID:  req.SubnetId,
	}, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListAddressesResponse{NextPageToken: nextToken}
	for _, a := range addrs {
		pb, err := addressToPb(a)
		if err != nil {
			return nil, err
		}
		resp.Addresses = append(resp.Addresses, pb)
	}
	return resp, nil
}

// Create — AuthZ → proto → CreateInput → use-case.
func (h *Handler) Create(ctx context.Context, req *vpcv1.CreateAddressRequest) (*operationpb.Operation, error) {
	if err := tenant.AssertProjectOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	in := CreateInput{
		ProjectID:          req.ProjectId,
		Name:               req.Name,
		Description:        req.Description,
		Labels:             req.Labels,
		DeletionProtection: req.DeletionProtection,
	}

	if ext := req.GetExternalIpv4AddressSpec(); ext != nil {
		in.ExternalSpec = &ExternalAddrSpec{
			Address: ext.Address,
			ZoneID:  ext.ZoneId,
		}
		if r := ext.GetRequirements(); r != nil {
			in.ExternalSpec.Requirements = &AddrRequirements{
				DdosProtectionProvider: r.DdosProtectionProvider,
				OutgoingSmtpCapability: r.OutgoingSmtpCapability,
			}
		}
	} else if intSpec := req.GetInternalIpv4AddressSpec(); intSpec != nil {
		in.InternalSpec = &InternalAddrSpec{
			Address:  intSpec.Address,
			SubnetID: intSpec.GetSubnetId(),
		}
	} else if int6Spec := req.GetInternalIpv6AddressSpec(); int6Spec != nil {
		in.InternalIpv6Spec = &InternalAddrSpec{
			Address:  int6Spec.Address,
			SubnetID: int6Spec.GetSubnetId(),
		}
	} else if ext6 := req.GetExternalIpv6AddressSpec(); ext6 != nil {
		// external IPv6 address.
		in.ExternalIpv6Spec = &ExternalAddrSpec{
			Address: ext6.Address,
			ZoneID:  ext6.ZoneId,
		}
		if r := ext6.GetRequirements(); r != nil {
			in.ExternalIpv6Spec.Requirements = &AddrRequirements{
				DdosProtectionProvider: r.DdosProtectionProvider,
				OutgoingSmtpCapability: r.OutgoingSmtpCapability,
			}
		}
	}

	op, err := h.create.Execute(ctx, in)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// Update — sync repo.Get + AuthZ + use-case.
func (h *Handler) Update(ctx context.Context, req *vpcv1.UpdateAddressRequest) (*operationpb.Operation, error) {
	if req.AddressId == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	a, err := h.get.Execute(ctx, "", req.AddressId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, a.ProjectID); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	op, err := h.update.Execute(ctx, UpdateInput{
		AddressID:          req.AddressId,
		Name:               req.Name,
		Description:        req.Description,
		Labels:             req.Labels,
		DeletionProtection: req.DeletionProtection,
		Reserved:           req.Reserved,
		UpdateMask:         mask,
	})
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// ListOperations — best-effort AuthZ: ресурс жив → project-ownership проверяем;
// удален (NotFound от get) → пропускаем (история операций должна оставаться
// доступной).
func (h *Handler) ListOperations(ctx context.Context, req *vpcv1.ListAddressOperationsRequest) (*vpcv1.ListAddressOperationsResponse, error) {
	if req.AddressId == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	if a, gerr := h.get.Execute(ctx, "", req.AddressId); gerr == nil {
		if err := tenant.AssertProjectOwnership(ctx, a.ProjectID); err != nil {
			return nil, err
		}
	} else if status.Code(gerr) != codes.NotFound {
		return nil, gerr
	}
	ops, nextToken, err := h.listOperations.Execute(ctx, req.AddressId, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListAddressOperationsResponse{NextPageToken: nextToken}
	for i := range ops {
		resp.Operations = append(resp.Operations, pbconv.OperationToProto(&ops[i]))
	}
	return resp, nil
}

// Delete — sync repo.Get для AuthZ, затем use-case.
func (h *Handler) Delete(ctx context.Context, req *vpcv1.DeleteAddressRequest) (*operationpb.Operation, error) {
	if req.AddressId == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	a, err := h.get.Execute(ctx, "", req.AddressId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, a.ProjectID); err != nil {
		return nil, err
	}
	op, err := h.delete.Execute(ctx, req.AddressId)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// addressToPb — repo-entity Address → proto Address через DTO-реестр.
func addressToPb(rec *kachorepo.AddressRecord) (*vpcv1.Address, error) {
	var dst *vpcv1.Address
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer Address failed")
	}
	return dst, nil
}
