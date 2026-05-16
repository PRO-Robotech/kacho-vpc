// Package cqrsadapter — тонкие port-adapter'ы поверх `kacho.Repository`,
// эмулирующие узкие port-интерфейсы, которые объявляют use-case-пакеты
// (`apps/kacho/api/<resource>/iface.go`) для admin-services и cross-resource
// peer-read'ов.
//
// KAC-94 A.7 ultra-final (skill evgeniy §1 A.7 + §6 G.1-G.7): после полного
// переезда сервиса на CQRS-Repository последняя зависимость на legacy
// `internal/repo/*_repo.go` — это именно эти узкие peer-port'ы (SubnetReader,
// NetworkRepo, AddressRepo, SecurityGroupRepo, NetworkInterfaceRepo и т.д.),
// которые ходили через legacy concrete-структуры. После замены на адаптеры
// поверх `kacho.Repository` legacy *_repo.go файлы можно удалить.
//
// Каждый адаптер открывает свежую Reader-TX на каждый вызов (G.4 — на slave-
// pool, если он настроен в kacho.Repository), что parity с тем, как делает
// каждый use-case в `apps/kacho/api/<resource>/`. Writer-TX используется только
// в `SecurityGroupAdapter.Insert`/`Delete` — для default-SG creation /
// cleanup в Network.Create/Delete. Outbox-emit в этих случаях кладётся в ту
// же writer-TX (атомарность DML + outbox).
package cqrsadapter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// =============================================================================
// Network
// =============================================================================

// NetworkAdapter удовлетворяет узким port-интерфейсам:
//   - `addresspool.NetworkRepo` / `securitygroup.NetworkReader` /
//     `privateendpoint.NetworkReader` / `networkinternal.NetworkRepo` —
//     все они объявляют как минимум `Get(id) (*kacho.NetworkRecord, error)`.
//   - `networkinternal.NetworkRepo` дополнительно требует `Update(domain.Network)`.
//
// Adapter открывает Reader-TX для read-методов; Update идёт в свежей writer-TX
// + outbox-emit Network.UPDATED.
type NetworkAdapter struct{ repo kacho.Repository }

// NewNetwork собирает NetworkAdapter поверх kacho.Repository.
func NewNetwork(r kacho.Repository) *NetworkAdapter { return &NetworkAdapter{repo: r} }

// Get — read через свежую Reader-TX.
func (a *NetworkAdapter) Get(ctx context.Context, id string) (*kacho.NetworkRecord, error) {
	rd, err := a.repo.Reader(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()
	return rd.Networks().Get(ctx, id)
}

// Update — DML + outbox-emit в одной writer-TX (atomicity G.5).
func (a *NetworkAdapter) Update(ctx context.Context, n *domain.Network) (*kacho.NetworkRecord, error) {
	w, err := a.repo.Writer(ctx)
	if err != nil {
		return nil, err
	}
	defer w.Abort()
	rec, err := w.Networks().Update(ctx, n)
	if err != nil {
		return nil, err
	}
	if err := w.Outbox().Emit(ctx, "Network", rec.ID, "UPDATED", networkPayloadMap(rec)); err != nil {
		return nil, fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err)
	}
	if err := w.Commit(); err != nil {
		return nil, err
	}
	return rec, nil
}

// =============================================================================
// Subnet
// =============================================================================

// SubnetAdapter удовлетворяет узким port-интерфейсам:
//   - `network.SubnetReader` (`List`),
//   - `address.SubnetReader` (`Get` + `AddressesBySubnet`),
//   - `addresspool.SubnetReader` (`Get`),
//   - `peapp.SubnetReader` (`Get`).
type SubnetAdapter struct{ repo kacho.Repository }

// NewSubnet собирает SubnetAdapter поверх kacho.Repository.
func NewSubnet(r kacho.Repository) *SubnetAdapter { return &SubnetAdapter{repo: r} }

// Get — read через свежую Reader-TX.
func (a *SubnetAdapter) Get(ctx context.Context, id string) (*kacho.SubnetRecord, error) {
	rd, err := a.repo.Reader(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()
	return rd.Subnets().Get(ctx, id)
}

// List — read через свежую Reader-TX.
func (a *SubnetAdapter) List(ctx context.Context, f kacho.SubnetFilter, p kacho.Pagination) ([]*kacho.SubnetRecord, string, error) {
	rd, err := a.repo.Reader(ctx)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rd.Close() }()
	return rd.Subnets().List(ctx, f, p)
}

// AddressesBySubnet — read через свежую Reader-TX. Используется
// `address.ListBySubnetUseCase` и `subnet.DeleteSubnetUseCase` precheck.
func (a *SubnetAdapter) AddressesBySubnet(ctx context.Context, subnetID string, p kacho.Pagination) ([]*kacho.AddressRecord, string, error) {
	rd, err := a.repo.Reader(ctx)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rd.Close() }()
	return rd.Subnets().AddressesBySubnet(ctx, subnetID, p)
}

// =============================================================================
// Address — full adapter (Get/SetReference/ClearReference/MarkEphemeralInUse/
// GetReference). Удовлетворяет:
//   - `networkinterface.AddressRepo` (Get + SetReference + ClearReference);
//   - `addresspool.AddressRepo` (Get);
//   - `addressref.Repo` (SetReference + MarkEphemeralInUse + ClearReference + GetReference);
//   - `subnet.AddressRefRepo` (ReferencesForAddresses).
// =============================================================================

// AddressAdapter удовлетворяет всем узким port-интерфейсам Address для admin/
// peer-сервисов. Каждый mutate-метод (SetReference / MarkEphemeralInUse /
// ClearReference) открывает свежую writer-TX. read-методы — свежая Reader-TX.
type AddressAdapter struct{ repo kacho.Repository }

// NewAddress собирает AddressAdapter поверх kacho.Repository.
func NewAddress(r kacho.Repository) *AddressAdapter { return &AddressAdapter{repo: r} }

// Get — read через свежую Reader-TX.
func (a *AddressAdapter) Get(ctx context.Context, id string) (*kacho.AddressRecord, error) {
	rd, err := a.repo.Reader(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()
	return rd.Addresses().Get(ctx, id)
}

// SetReference — atomic CAS-upsert referrer-row + addresses.used=true. Свежая
// writer-TX (sync-операция от addressref / NIC use-case). Outbox НЕ emit'ит —
// referrer-tracking не публичное событие (как раньше в legacy AddressRepo).
func (a *AddressAdapter) SetReference(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error) {
	w, err := a.repo.Writer(ctx)
	if err != nil {
		return nil, err
	}
	defer w.Abort()
	out, err := w.Addresses().SetReference(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := w.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// MarkEphemeralInUse — атомарно reserved=false + used=true + upsert referrer.
// Свежая writer-TX.
func (a *AddressAdapter) MarkEphemeralInUse(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error) {
	w, err := a.repo.Writer(ctx)
	if err != nil {
		return nil, err
	}
	defer w.Abort()
	out, err := w.Addresses().MarkEphemeralInUse(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := w.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// ClearReference — удаляет referrer-row + used=false. Свежая writer-TX.
func (a *AddressAdapter) ClearReference(ctx context.Context, addressID string) error {
	w, err := a.repo.Writer(ctx)
	if err != nil {
		return err
	}
	defer w.Abort()
	if err := w.Addresses().ClearReference(ctx, addressID); err != nil {
		return err
	}
	return w.Commit()
}

// GetReference — read через свежую Reader-TX.
func (a *AddressAdapter) GetReference(ctx context.Context, addressID string) (*domain.AddressReference, error) {
	rd, err := a.repo.Reader(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()
	return rd.Addresses().GetReference(ctx, addressID)
}

// ReferencesForAddresses — batch read через свежую Reader-TX.
func (a *AddressAdapter) ReferencesForAddresses(ctx context.Context, ids []string) (map[string]*domain.AddressReference, error) {
	rd, err := a.repo.Reader(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()
	return rd.Addresses().ReferencesForAddresses(ctx, ids)
}

// =============================================================================
// RouteTable
// =============================================================================

// RouteTableAdapter удовлетворяет узким port-интерфейсам:
//   - `network.RouteTableReader` (`List`).
type RouteTableAdapter struct{ repo kacho.Repository }

// NewRouteTable собирает RouteTableAdapter поверх kacho.Repository.
func NewRouteTable(r kacho.Repository) *RouteTableAdapter { return &RouteTableAdapter{repo: r} }

// List — read через свежую Reader-TX.
func (a *RouteTableAdapter) List(ctx context.Context, f kacho.RouteTableFilter, p kacho.Pagination) ([]*kacho.RouteTableRecord, string, error) {
	rd, err := a.repo.Reader(ctx)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rd.Close() }()
	return rd.RouteTables().List(ctx, f, p)
}

// =============================================================================
// SecurityGroup
// =============================================================================

// SecurityGroupAdapter удовлетворяет узким port-интерфейсам:
//   - `network.SecurityGroupRepo` (`List` + `Insert` + `Delete` для default-SG
//     creation/cleanup в Network.Create/Delete);
//   - `networkinternal.SecurityGroupRepo` (`Get`).
//
// Insert и Delete используют отдельные writer-TX с outbox-emit (parity с
// поведением legacy `*repo.SecurityGroupRepo` + manual outbox-emit на стороне
// caller'а — теперь всё в одной TX adapter'а).
type SecurityGroupAdapter struct{ repo kacho.Repository }

// NewSecurityGroup собирает SecurityGroupAdapter поверх kacho.Repository.
func NewSecurityGroup(r kacho.Repository) *SecurityGroupAdapter {
	return &SecurityGroupAdapter{repo: r}
}

// Get — read через свежую Reader-TX.
func (a *SecurityGroupAdapter) Get(ctx context.Context, id string) (*kacho.SecurityGroupRecord, error) {
	rd, err := a.repo.Reader(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()
	return rd.SecurityGroups().Get(ctx, id)
}

// List — read через свежую Reader-TX.
func (a *SecurityGroupAdapter) List(ctx context.Context, f kacho.SecurityGroupFilter, p kacho.Pagination) ([]*kacho.SecurityGroupRecord, string, error) {
	rd, err := a.repo.Reader(ctx)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rd.Close() }()
	return rd.SecurityGroups().List(ctx, f, p)
}

// Insert — DML + outbox-emit CREATED в одной writer-TX. Используется
// `network.CreateNetworkUseCase` для inline default-SG creation.
func (a *SecurityGroupAdapter) Insert(ctx context.Context, sg *domain.SecurityGroup) (*kacho.SecurityGroupRecord, error) {
	w, err := a.repo.Writer(ctx)
	if err != nil {
		return nil, err
	}
	defer w.Abort()
	rec, err := w.SecurityGroups().Insert(ctx, sg)
	if err != nil {
		return nil, err
	}
	if err := w.Outbox().Emit(ctx, "SecurityGroup", rec.ID, "CREATED", securityGroupPayloadMap(rec)); err != nil {
		return nil, fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err)
	}
	if err := w.Commit(); err != nil {
		return nil, err
	}
	return rec, nil
}

// Delete — DML + outbox-emit DELETED в одной writer-TX. Используется
// `network.DeleteNetworkUseCase` для default-SG cleanup перед Network.Delete.
func (a *SecurityGroupAdapter) Delete(ctx context.Context, id string) error {
	w, err := a.repo.Writer(ctx)
	if err != nil {
		return err
	}
	defer w.Abort()
	if err := w.SecurityGroups().Delete(ctx, id); err != nil {
		return err
	}
	if err := w.Outbox().Emit(ctx, "SecurityGroup", id, "DELETED", map[string]any{"id": id}); err != nil {
		return fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err)
	}
	return w.Commit()
}

// =============================================================================
// NetworkInterface
// =============================================================================

// NetworkInterfaceAdapter удовлетворяет узким port-интерфейсам:
//   - `subnet.NetworkInterfaceRepo` (`ListBySubnet`).
type NetworkInterfaceAdapter struct{ repo kacho.Repository }

// NewNetworkInterface собирает NetworkInterfaceAdapter поверх kacho.Repository.
func NewNetworkInterface(r kacho.Repository) *NetworkInterfaceAdapter {
	return &NetworkInterfaceAdapter{repo: r}
}

// ListBySubnet — read через свежую Reader-TX.
func (a *NetworkInterfaceAdapter) ListBySubnet(ctx context.Context, subnetID string) ([]*kacho.NetworkInterfaceRecord, error) {
	rd, err := a.repo.Reader(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()
	return rd.NetworkInterfaces().ListBySubnet(ctx, subnetID)
}

// =============================================================================
// Payload helpers — копии из соответствующих use-case-пакетов (мы не можем
// импортировать `apps/kacho/api/network` etc. отсюда из-за обратной зависимости).
// Поля — те же, что эмитят legacy `*repo.NetworkRepo.Insert/Update` / `*repo.
// SecurityGroupRepo.Insert/Delete` помощниками `vpcoutbox.Emit*`.
// =============================================================================

// networkPayloadMap — snapshot Network для outbox-payload (parity с
// `apps/kacho/api/network/helpers.go::networkPayloadMap`: JSON round-trip
// сериализация всей entity-структуры).
func networkPayloadMap(n *kacho.NetworkRecord) map[string]any {
	return jsonRoundTrip(n)
}

// securityGroupPayloadMap — snapshot SG для outbox-payload (parity с
// `apps/kacho/api/securitygroup/helpers.go::securityGroupPayloadMap`).
func securityGroupPayloadMap(sg *kacho.SecurityGroupRecord) map[string]any {
	return jsonRoundTrip(sg)
}

// jsonRoundTrip — общий helper «struct → JSON → map[string]any». В случае
// ошибки возвращает пустой map (outbox-payload это не критическое поле — пусть
// пустое, чем падать). Parity с `helpers.go::*payloadMap` use-case-пакетов.
func jsonRoundTrip(v any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{}
	}
	return m
}
