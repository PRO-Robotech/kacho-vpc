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

func (nw *networkInterfaceWriter) Insert(_ context.Context, n *domain.NetworkInterface) (*kacho.NetworkInterfaceRecord, error) {
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

// Compile-time проверка соответствия интерфейсам.
var (
	_ kacho.NetworkInterfaceReaderIface = (*networkInterfaceReader)(nil)
	_ kacho.NetworkInterfaceWriterIface = (*networkInterfaceWriter)(nil)
)
