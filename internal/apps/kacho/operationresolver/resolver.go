// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package operationresolver — доменный resolver осиротевших LRO для kacho-vpc.
//
// Движок reconciler'а живет в kacho-corelib/operations (он сканирует таблицу
// operations по grace-окну и клеймит orphan'ы под FOR UPDATE SKIP LOCKED). Сам
// resolver — доменная часть в сервисе: он знает типы метаданных VPC
// (*vpcv1.<Verb><Resource>Metadata) и сверяет осиротевшую операцию с
// committed-реальностью ресурса через repo.Get.
//
// Контракт диспетчеризации (api-conventions / data-integrity: writer-TX
// атомарна, частичных состояний нет):
//   - Create/Update-метаданные: ресурс присутствует → Done(current ресурс как
//     Response); отсутствует → Interrupted.
//   - Delete-метаданные: ресурс отсутствует → Done(Empty); присутствует →
//     Interrupted.
//   - неузнанный / nil тип метаданных → Skip (строка остается done=false, sweep
//     повторится);
//   - transient-ошибка чтения ресурса → (ResolverResult{}, err): движок
//     инкрементит reconcile_errors и пропускает orphan до следующего sweep'а.
//
// Resolver не делает re-drive (повторный запуск worker-fn) — он приводит статус
// операции в соответствие тому, что реально закоммичено.
package operationresolver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/operations"

	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	// Blank-import регистрирует record→proto трансферы (Network/Subnet/...),
	// которыми resolver упаковывает текущий ресурс в Operation.response.
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
)

// kind — категория операции, выводимая из типа метаданных.
type kind int

const (
	kindCreate kind = iota // present → Done(current); absent → Interrupted
	kindUpdate             // как Create (reconcile к committed-реальности, не re-apply)
	kindDelete             // absent → Done(Empty); present → Interrupted
)

// Resolver — доменный resolver VPC поверх CQRS-Repository. Зависит только от
// read-порта репозитория (kacho.Repository.Reader) — adapter инжектится
// composition root'ом; pgx/grpc resolver не импортирует.
type Resolver struct {
	repo kachorepo.Repository
	log  *slog.Logger
}

// Option — функциональная опция Resolver.
type Option func(*Resolver)

// WithLogger подключает структурированный логгер (диагностика resolve).
func WithLogger(l *slog.Logger) Option {
	return func(r *Resolver) {
		if l != nil {
			r.log = l
		}
	}
}

// New конструирует Resolver поверх CQRS-Repository.
func New(repo kachorepo.Repository, opts ...Option) *Resolver {
	r := &Resolver{repo: repo, log: slog.Default()}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Resolve реализует operations.Resolver: по метаданным осиротевшей операции
// определяет терминальный исход, сверяясь с committed-реальностью ресурса.
func (r *Resolver) Resolve(ctx context.Context, op operations.Operation) (operations.ResolverResult, error) {
	if op.Metadata == nil {
		return skip(), nil
	}
	msg, err := op.Metadata.UnmarshalNew()
	if err != nil {
		// Неизвестный / неразбираемый тип метаданных — не наша операция в этом
		// прогоне. Skip, а не ошибка: строка остается done=false.
		r.log.Warn("operation resolver: undecodable metadata, skipping orphan",
			"op", op.ID, "type_url", op.Metadata.TypeUrl, "err", err)
		return skip(), nil
	}

	rd, err := r.repo.Reader(ctx)
	if err != nil {
		return operations.ResolverResult{}, fmt.Errorf("operationresolver: open reader: %w", err)
	}
	defer func() { _ = rd.Close() }()

	switch m := msg.(type) {
	case *vpcv1.CreateNetworkMetadata:
		return resolveExistence(ctx, kindCreate, m.GetNetworkId(), rd.Networks().Get, marshalNetwork)
	case *vpcv1.UpdateNetworkMetadata:
		return resolveExistence(ctx, kindUpdate, m.GetNetworkId(), rd.Networks().Get, marshalNetwork)
	case *vpcv1.DeleteNetworkMetadata:
		return resolveExistence(ctx, kindDelete, m.GetNetworkId(), rd.Networks().Get, marshalNetwork)

	case *vpcv1.CreateSubnetMetadata:
		return resolveExistence(ctx, kindCreate, m.GetSubnetId(), rd.Subnets().Get, marshalSubnet)
	case *vpcv1.UpdateSubnetMetadata:
		return resolveExistence(ctx, kindUpdate, m.GetSubnetId(), rd.Subnets().Get, marshalSubnet)
	case *vpcv1.DeleteSubnetMetadata:
		return resolveExistence(ctx, kindDelete, m.GetSubnetId(), rd.Subnets().Get, marshalSubnet)

	case *vpcv1.CreateAddressMetadata:
		return resolveExistence(ctx, kindCreate, m.GetAddressId(), rd.Addresses().Get, marshalAddress)
	case *vpcv1.UpdateAddressMetadata:
		return resolveExistence(ctx, kindUpdate, m.GetAddressId(), rd.Addresses().Get, marshalAddress)
	case *vpcv1.DeleteAddressMetadata:
		return resolveExistence(ctx, kindDelete, m.GetAddressId(), rd.Addresses().Get, marshalAddress)

	case *vpcv1.CreateRouteTableMetadata:
		return resolveExistence(ctx, kindCreate, m.GetRouteTableId(), rd.RouteTables().Get, marshalRouteTable)
	case *vpcv1.UpdateRouteTableMetadata:
		return resolveExistence(ctx, kindUpdate, m.GetRouteTableId(), rd.RouteTables().Get, marshalRouteTable)
	case *vpcv1.DeleteRouteTableMetadata:
		return resolveExistence(ctx, kindDelete, m.GetRouteTableId(), rd.RouteTables().Get, marshalRouteTable)

	case *vpcv1.CreateSecurityGroupMetadata:
		return resolveExistence(ctx, kindCreate, m.GetSecurityGroupId(), rd.SecurityGroups().Get, marshalSecurityGroup)
	case *vpcv1.UpdateSecurityGroupMetadata:
		return resolveExistence(ctx, kindUpdate, m.GetSecurityGroupId(), rd.SecurityGroups().Get, marshalSecurityGroup)
	case *vpcv1.UpdateSecurityGroupRuleMetadata:
		return resolveExistence(ctx, kindUpdate, m.GetSecurityGroupId(), rd.SecurityGroups().Get, marshalSecurityGroup)
	case *vpcv1.DeleteSecurityGroupMetadata:
		return resolveExistence(ctx, kindDelete, m.GetSecurityGroupId(), rd.SecurityGroups().Get, marshalSecurityGroup)

	case *vpcv1.CreateGatewayMetadata:
		return resolveExistence(ctx, kindCreate, m.GetGatewayId(), rd.Gateways().Get, marshalGateway)
	case *vpcv1.UpdateGatewayMetadata:
		return resolveExistence(ctx, kindUpdate, m.GetGatewayId(), rd.Gateways().Get, marshalGateway)
	case *vpcv1.DeleteGatewayMetadata:
		return resolveExistence(ctx, kindDelete, m.GetGatewayId(), rd.Gateways().Get, marshalGateway)

	case *vpcv1.CreateNetworkInterfaceMetadata:
		return resolveExistence(ctx, kindCreate, m.GetNetworkInterfaceId(), rd.NetworkInterfaces().Get, marshalNetworkInterface)
	case *vpcv1.UpdateNetworkInterfaceMetadata:
		return resolveExistence(ctx, kindUpdate, m.GetNetworkInterfaceId(), rd.NetworkInterfaces().Get, marshalNetworkInterface)
	case *vpcv1.DeleteNetworkInterfaceMetadata:
		return resolveExistence(ctx, kindDelete, m.GetNetworkInterfaceId(), rd.NetworkInterfaces().Get, marshalNetworkInterface)

	default:
		// AddressPool — admin-only sync-ресурс (без LRO), прочие типы — не наши.
		return skip(), nil
	}
}

// resolveExistence — общая логика «существование ресурса → терминальный исход».
// get читает ресурс в открытой read-TX (repo.ErrNotFound → отсутствует), toAny
// упаковывает текущий ресурс в Operation.response для Done на Create/Update.
func resolveExistence[T any](
	ctx context.Context,
	k kind,
	id string,
	get func(context.Context, string) (*T, error),
	toAny func(*T) (*anypb.Any, error),
) (operations.ResolverResult, error) {
	rec, err := get(ctx, id)
	switch {
	case err == nil:
		// present
	case errors.Is(err, repo.ErrNotFound):
		rec = nil // absent
	default:
		// transient read-ошибка → движок инкрементит reconcile_errors, пропускает.
		return operations.ResolverResult{}, fmt.Errorf("operationresolver: get %q: %w", id, err)
	}

	present := rec != nil
	if k == kindDelete {
		if present {
			return interrupted(), nil
		}
		return done(nil), nil // Empty-семантика
	}
	// Create / Update.
	if !present {
		return interrupted(), nil
	}
	resp, err := toAny(rec)
	if err != nil {
		return operations.ResolverResult{}, fmt.Errorf("operationresolver: marshal %q: %w", id, err)
	}
	return done(resp), nil
}

func skip() operations.ResolverResult {
	return operations.ResolverResult{Outcome: operations.OutcomeSkip}
}

func done(resp *anypb.Any) operations.ResolverResult {
	return operations.ResolverResult{Outcome: operations.OutcomeDone, Response: resp}
}

func interrupted() operations.ResolverResult {
	return operations.ResolverResult{Outcome: operations.OutcomeInterrupted}
}

// ---- record → Any маршалеры (через DTO-реестр) ----
//
// DTO-реестр (dto.Transfer) типизирован закрытым union'ом пар (record, proto),
// поэтому трансфер вызывается на КОНКРЕТНЫХ типах в каждом маршалере (generic-
// обертка не прошла бы constraint Transferrable).

func marshalNetwork(rec *kachorepo.NetworkRecord) (*anypb.Any, error) {
	var dst *vpcv1.Network
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, err
	}
	return anypb.New(dst)
}

func marshalSubnet(rec *kachorepo.SubnetRecord) (*anypb.Any, error) {
	var dst *vpcv1.Subnet
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, err
	}
	return anypb.New(dst)
}

func marshalAddress(rec *kachorepo.AddressRecord) (*anypb.Any, error) {
	var dst *vpcv1.Address
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, err
	}
	return anypb.New(dst)
}

func marshalRouteTable(rec *kachorepo.RouteTableRecord) (*anypb.Any, error) {
	var dst *vpcv1.RouteTable
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, err
	}
	return anypb.New(dst)
}

func marshalSecurityGroup(rec *kachorepo.SecurityGroupRecord) (*anypb.Any, error) {
	var dst *vpcv1.SecurityGroup
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, err
	}
	return anypb.New(dst)
}

func marshalGateway(rec *kachorepo.GatewayRecord) (*anypb.Any, error) {
	var dst *vpcv1.Gateway
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, err
	}
	return anypb.New(dst)
}

func marshalNetworkInterface(rec *kachorepo.NetworkInterfaceRecord) (*anypb.Any, error) {
	var dst *vpcv1.NetworkInterface
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, err
	}
	return anypb.New(dst)
}

// Compile-time: Resolver удовлетворяет corelib-порту operations.Resolver.
var _ operations.Resolver = (*Resolver)(nil)
