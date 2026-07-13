// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kachomock

import (
	"context"
	"sort"
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// In-memory NetworkInterface reader/writer для kachomock. Файл вынесен из
// `repository.go` отдельно — parity с `address.go` / `route_table.go`.
//
// NIC — самостоятельный сетевой интерфейс (first-class, отвязан от Instance),
// самый «толстый» ресурс VPC. Mock здесь покрывает:
//   - CRUD (Insert / UpdateMeta / Delete);
//   - ListBySubnet — для SubnetService.Delete precheck (NIC жестко блокирует
//     свою подсеть через ON DELETE RESTRICT).
//
// MAC-allocation в mock не моделируется (caller-side responsibility:
// `service.doCreate` ставит mac через `macutil.GenerateMAC` и retry'ит на
// UNIQUE-collision на mac_address; mock-Insert просто принимает что есть).

// ---- NetworkInterface reader ----

// networkInterfaceReader — read-only snapshot NIC.
type networkInterfaceReader struct {
	snap map[string]*kacho.NetworkInterfaceRecord
}

func (r *networkInterfaceReader) Get(_ context.Context, id string) (*kacho.NetworkInterfaceRecord, error) {
	n, ok := r.snap[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *n
	return &cp, nil
}

func (r *networkInterfaceReader) List(_ context.Context, f kacho.NetworkInterfaceFilter, _ kacho.Pagination) ([]*kacho.NetworkInterfaceRecord, string, error) {
	var result []*kacho.NetworkInterfaceRecord
	for _, n := range r.snap {
		if (f.ProjectID != "" && n.ProjectID != f.ProjectID) ||
			(f.SubnetID != "" && n.SubnetID != f.SubnetID) ||
			(f.InstanceID != "" && (n.UsedByType != "compute_instance" || n.UsedByID != f.InstanceID)) {
			continue
		}
		cp := *n
		result = append(result, &cp)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

// ListByIDs — фильтрация поверх множества разрешенных ids + те же in-memory
// предикаты, что и в List (project_id/subnet_id/instance_id по used_by).
// Пустой allowedIDs → (nil, "", nil).
func (r *networkInterfaceReader) ListByIDs(_ context.Context, f kacho.NetworkInterfaceFilter, allowedIDs []string, _ kacho.Pagination) ([]*kacho.NetworkInterfaceRecord, string, error) {
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	allowed := make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id] = struct{}{}
	}
	var result []*kacho.NetworkInterfaceRecord
	for _, n := range r.snap {
		if _, ok := allowed[n.ID]; !ok {
			continue
		}
		if (f.ProjectID != "" && n.ProjectID != f.ProjectID) ||
			(f.SubnetID != "" && n.SubnetID != f.SubnetID) ||
			(f.InstanceID != "" && (n.UsedByType != "compute_instance" || n.UsedByID != f.InstanceID)) {
			continue
		}
		cp := *n
		result = append(result, &cp)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

func (r *networkInterfaceReader) ListBySubnet(_ context.Context, subnetID string) ([]*kacho.NetworkInterfaceRecord, error) {
	var result []*kacho.NetworkInterfaceRecord
	for _, n := range r.snap {
		if n.SubnetID == subnetID {
			cp := *n
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// ListByInstanceIDs — in-memory batched read NIC-привязок. Index не моделируется
// (used_by_index — DB-колонка, вне domain-записи) → всегда 0; primary-адреса не
// резолвятся (join addresses — только в pg). Слот/адрес-семантика — в integration.
func (r *networkInterfaceReader) ListByInstanceIDs(_ context.Context, instanceIDs []string) ([]*kacho.NetworkInterfaceAttachment, error) {
	if len(instanceIDs) == 0 {
		return nil, nil
	}
	want := make(map[string]struct{}, len(instanceIDs))
	for _, id := range instanceIDs {
		want[id] = struct{}{}
	}
	var out []*kacho.NetworkInterfaceAttachment
	for _, n := range r.snap {
		if n.UsedByType != "compute_instance" {
			continue
		}
		if _, ok := want[n.UsedByID]; !ok {
			continue
		}
		out = append(out, &kacho.NetworkInterfaceAttachment{
			NICID: n.ID, InstanceID: n.UsedByID, SubnetID: n.SubnetID,
			SecurityGroupIDs: n.SecurityGroupIDs, MAC: n.MAC,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NICID < out[j].NICID })
	return out, nil
}

// ---- NetworkInterface writer ----

// networkInterfaceWriter — write-«TX» NIC. Writer видит свои writes —
// Get/List поверх localNIs.
type networkInterfaceWriter struct {
	w *writerImpl
}

func (nw *networkInterfaceWriter) Get(_ context.Context, id string) (*kacho.NetworkInterfaceRecord, error) {
	if _, deleted := nw.w.deletedNIIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	n, ok := nw.w.localNIs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *n
	return &cp, nil
}

// GetForUpdate — in-memory mock не моделирует row-lock; делегирует Get
// (сериализация проверяется integration-тестом на реальном Postgres).
func (nw *networkInterfaceWriter) GetForUpdate(ctx context.Context, id string) (*kacho.NetworkInterfaceRecord, error) {
	return nw.Get(ctx, id)
}

func (nw *networkInterfaceWriter) List(_ context.Context, f kacho.NetworkInterfaceFilter, _ kacho.Pagination) ([]*kacho.NetworkInterfaceRecord, string, error) {
	var result []*kacho.NetworkInterfaceRecord
	for id, n := range nw.w.localNIs {
		if _, deleted := nw.w.deletedNIIDs[id]; deleted {
			continue
		}
		if (f.ProjectID != "" && n.ProjectID != f.ProjectID) ||
			(f.SubnetID != "" && n.SubnetID != f.SubnetID) ||
			(f.InstanceID != "" && (n.UsedByType != "compute_instance" || n.UsedByID != f.InstanceID)) {
			continue
		}
		cp := *n
		result = append(result, &cp)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

// ListByIDs — writer-side: фильтрация поверх множества разрешенных ids + те же
// in-memory предикаты, что и в List. Пустой allowedIDs → (nil, "", nil).
func (nw *networkInterfaceWriter) ListByIDs(_ context.Context, f kacho.NetworkInterfaceFilter, allowedIDs []string, _ kacho.Pagination) ([]*kacho.NetworkInterfaceRecord, string, error) {
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	allowed := make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id] = struct{}{}
	}
	var result []*kacho.NetworkInterfaceRecord
	for id, n := range nw.w.localNIs {
		if _, deleted := nw.w.deletedNIIDs[id]; deleted {
			continue
		}
		if _, ok := allowed[id]; !ok {
			continue
		}
		if (f.ProjectID != "" && n.ProjectID != f.ProjectID) ||
			(f.SubnetID != "" && n.SubnetID != f.SubnetID) ||
			(f.InstanceID != "" && (n.UsedByType != "compute_instance" || n.UsedByID != f.InstanceID)) {
			continue
		}
		cp := *n
		result = append(result, &cp)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

func (nw *networkInterfaceWriter) ListBySubnet(_ context.Context, subnetID string) ([]*kacho.NetworkInterfaceRecord, error) {
	var result []*kacho.NetworkInterfaceRecord
	for id, n := range nw.w.localNIs {
		if _, deleted := nw.w.deletedNIIDs[id]; deleted {
			continue
		}
		if n.SubnetID == subnetID {
			cp := *n
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// ListByInstanceIDs — writer-side batched read NIC-привязок (parity с pg-writer,
// который видит свои writes). Index не моделируется (см. reader).
func (nw *networkInterfaceWriter) ListByInstanceIDs(_ context.Context, instanceIDs []string) ([]*kacho.NetworkInterfaceAttachment, error) {
	if len(instanceIDs) == 0 {
		return nil, nil
	}
	want := make(map[string]struct{}, len(instanceIDs))
	for _, id := range instanceIDs {
		want[id] = struct{}{}
	}
	var out []*kacho.NetworkInterfaceAttachment
	for id, n := range nw.w.localNIs {
		if _, deleted := nw.w.deletedNIIDs[id]; deleted {
			continue
		}
		if n.UsedByType != "compute_instance" {
			continue
		}
		if _, ok := want[n.UsedByID]; !ok {
			continue
		}
		out = append(out, &kacho.NetworkInterfaceAttachment{
			NICID: n.ID, InstanceID: n.UsedByID, SubnetID: n.SubnetID,
			SecurityGroupIDs: n.SecurityGroupIDs, MAC: n.MAC,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NICID < out[j].NICID })
	return out, nil
}

func (nw *networkInterfaceWriter) Insert(_ context.Context, n *domain.NetworkInterface) (*kacho.NetworkInterfaceRecord, error) {
	// Тест-хук mac-collision: если установлен, вызывается перед вставкой с текущим
	// MAC. Ненулевая ошибка (обычно repo.ErrMacCollision) заменяет вставку — так
	// unit-тест гоняет retry-петлю use-case'а (mock не моделирует UNIQUE mac_address).
	// Хук выставляется до старта worker-goroutine, гонки на чтении нет.
	if hook := nw.w.parent.niInsertHook; hook != nil {
		if err := hook(n.MAC); err != nil {
			return nil, err
		}
	}
	rec := &kacho.NetworkInterfaceRecord{NetworkInterface: *n, CreatedAt: time.Now().UTC()}
	nw.w.localNIs[n.ID] = rec
	cp := *rec
	return &cp, nil
}

func (nw *networkInterfaceWriter) UpdateMeta(_ context.Context, n *domain.NetworkInterface) (*kacho.NetworkInterfaceRecord, error) {
	if _, deleted := nw.w.deletedNIIDs[n.ID]; deleted {
		return nil, repo.ErrNotFound
	}
	existing, ok := nw.w.localNIs[n.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	// Обновляем mutable-поля (parity с pg-impl): name/description/labels/
	// security_group_ids/v4_address_ids/v6_address_ids. Immutable: project_id/
	// subnet_id/mac_address.
	existing.Name = n.Name
	existing.Description = n.Description
	existing.Labels = n.Labels
	existing.SecurityGroupIDs = n.SecurityGroupIDs
	existing.V4AddressIDs = n.V4AddressIDs
	existing.V6AddressIDs = n.V6AddressIDs
	cp := *existing
	return &cp, nil
}

func (nw *networkInterfaceWriter) Delete(_ context.Context, id string) error {
	if _, ok := nw.w.localNIs[id]; !ok {
		return repo.ErrNotFound
	}
	if nw.w.deletedNIIDs == nil {
		nw.w.deletedNIIDs = make(map[string]struct{})
	}
	nw.w.deletedNIIDs[id] = struct{}{}
	delete(nw.w.localNIs, id)
	return nil
}

// AttachToInstance — in-memory CAS (used_by_id=” OR =$instance). Zone-coherence и
// slot-index не моделируются (нужны subnets/UNIQUE-индекс — только в pg); проверяются
// integration-тестами. Здесь — used_by CAS + idempotent replay + in-use sentinel.
func (nw *networkInterfaceWriter) AttachToInstance(_ context.Context, p kacho.AttachNICParams) (*kacho.NetworkInterfaceRecord, error) {
	if _, deleted := nw.w.deletedNIIDs[p.NICID]; deleted {
		return nil, repo.ErrNotFound
	}
	n, ok := nw.w.localNIs[p.NICID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	if n.UsedByID != "" && n.UsedByID != p.InstanceID {
		return nil, repo.ErrNICInUse
	}
	n.UsedByType = "compute_instance"
	n.UsedByID = p.InstanceID
	n.UsedByName = p.InstanceName
	n.Status = domain.NIStatusActive
	cp := *n
	return &cp, nil
}

// DetachFromInstance — in-memory идемпотентное снятие привязки.
func (nw *networkInterfaceWriter) DetachFromInstance(ctx context.Context, nicID, instanceID string) (*kacho.NetworkInterfaceRecord, error) {
	if _, deleted := nw.w.deletedNIIDs[nicID]; deleted {
		return nil, repo.ErrNotFound
	}
	n, ok := nw.w.localNIs[nicID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	if n.UsedByID == instanceID {
		n.UsedByType = ""
		n.UsedByID = ""
		n.UsedByName = ""
		n.Status = domain.NIStatusAvailable
	}
	cp := *n
	return &cp, nil
}

// Compile-time проверка соответствия интерфейсам.
var (
	_ kacho.NetworkInterfaceReaderIface = (*networkInterfaceReader)(nil)
	_ kacho.NetworkInterfaceWriterIface = (*networkInterfaceWriter)(nil)
)
