package kachomock

import (
	"context"
	"sort"
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// In-memory SecurityGroup reader/writer для kachomock. Wave 5 replicate (KAC-94,
// skill evgeniy §6 G.7): полное покрытие 8 ресурсов VPC kachomock-ом. Файл
// вынесен из `repository.go` отдельно — parity с `address.go` / `route_table.go`.
//
// SG-specific operations:
//   - UpdateRules / UpdateRule — упрощённая семантика (без xmin-OCC; mock не
//     моделирует concurrent-conflict; pg-impl-side OCC покрывается
//     integration-тестом `security_group_occ_integration_test.go`).
//   - SG используется inline в Network.Create при `KACHO_VPC_DEFAULT_SG_INLINE=true`
//     (default) — default SG создаётся в той же writer-TX, что и Network.

// ---- SecurityGroup reader ----

// securityGroupReader — read-only snapshot SG.
// nics/nets — для derived-on-read UsedBy (KAC-239 S2).
type securityGroupReader struct {
	snap map[string]*kacho.SecurityGroupRecord
	nics map[string]*kacho.NetworkInterfaceRecord
	nets map[string]*kacho.NetworkRecord
}

func (r *securityGroupReader) Get(_ context.Context, id string) (*kacho.SecurityGroupRecord, error) {
	sg, ok := r.snap[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *sg
	return &cp, nil
}

func (r *securityGroupReader) List(_ context.Context, f kacho.SecurityGroupFilter, _ kacho.Pagination) ([]*kacho.SecurityGroupRecord, string, error) {
	var result []*kacho.SecurityGroupRecord
	for _, sg := range r.snap {
		if (f.ProjectID == "" || sg.ProjectID == f.ProjectID) &&
			(f.NetworkID == "" || sg.NetworkID == f.NetworkID) &&
			(f.Name == "" || string(sg.Name) == f.Name) {
			cp := *sg
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

// UsedBy — KAC-239 S2: потребители SG (derived-on-read), parity с pg-impl.
func (r *securityGroupReader) UsedBy(_ context.Context, sgID string) ([]domain.SecurityGroupReference, error) {
	return computeSGUsedBy(sgID, r.nics, r.nets)
}

// computeSGUsedBy — общий derived-on-read скан для reader/writer mock'а:
// NIC.security_group_ids ∋ sgID (тип "network_interface") +
// networks.default_security_group_id == sgID (тип "network"); сорт по id.
func computeSGUsedBy(
	sgID string,
	nics map[string]*kacho.NetworkInterfaceRecord,
	nets map[string]*kacho.NetworkRecord,
) ([]domain.SecurityGroupReference, error) {
	var nicIDs, netIDs []string
	for id, nic := range nics {
		for _, sg := range nic.SecurityGroupIDs {
			if sg == sgID {
				nicIDs = append(nicIDs, id)
				break
			}
		}
	}
	for id, n := range nets {
		if n.DefaultSecurityGroupID == sgID {
			netIDs = append(netIDs, id)
		}
	}
	sort.Strings(nicIDs)
	sort.Strings(netIDs)
	var out []domain.SecurityGroupReference
	for _, id := range nicIDs {
		out = append(out, domain.SecurityGroupReference{ReferrerType: "network_interface", ReferrerID: id})
	}
	for _, id := range netIDs {
		out = append(out, domain.SecurityGroupReference{ReferrerType: "network", ReferrerID: id})
	}
	return out, nil
}

// ---- SecurityGroup writer ----

// securityGroupWriter — write-«TX» SG. Writer видит свои writes (G.2) —
// Get/List поверх localSGs.
type securityGroupWriter struct {
	w *writerImpl
}

func (sw *securityGroupWriter) Get(_ context.Context, id string) (*kacho.SecurityGroupRecord, error) {
	if _, deleted := sw.w.deletedSGIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	sg, ok := sw.w.localSGs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *sg
	return &cp, nil
}

func (sw *securityGroupWriter) List(_ context.Context, f kacho.SecurityGroupFilter, _ kacho.Pagination) ([]*kacho.SecurityGroupRecord, string, error) {
	var result []*kacho.SecurityGroupRecord
	for id, sg := range sw.w.localSGs {
		if _, deleted := sw.w.deletedSGIDs[id]; deleted {
			continue
		}
		if (f.ProjectID == "" || sg.ProjectID == f.ProjectID) &&
			(f.NetworkID == "" || sg.NetworkID == f.NetworkID) &&
			(f.Name == "" || string(sg.Name) == f.Name) {
			cp := *sg
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, "", nil
}

func (sw *securityGroupWriter) Insert(_ context.Context, sg *domain.SecurityGroup) (*kacho.SecurityGroupRecord, error) {
	rec := &kacho.SecurityGroupRecord{SecurityGroup: *sg, CreatedAt: time.Now().UTC()}
	sw.w.localSGs[sg.ID] = rec
	cp := *rec
	return &cp, nil
}

func (sw *securityGroupWriter) Update(_ context.Context, sg *domain.SecurityGroup) (*kacho.SecurityGroupRecord, error) {
	if _, deleted := sw.w.deletedSGIDs[sg.ID]; deleted {
		return nil, repo.ErrNotFound
	}
	existing, ok := sw.w.localSGs[sg.ID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	existing.SecurityGroup = *sg
	cp := *existing
	return &cp, nil
}

func (sw *securityGroupWriter) Delete(_ context.Context, id string) error {
	if _, ok := sw.w.localSGs[id]; !ok {
		return repo.ErrNotFound
	}
	if sw.w.deletedSGIDs == nil {
		sw.w.deletedSGIDs = make(map[string]struct{})
	}
	sw.w.deletedSGIDs[id] = struct{}{}
	delete(sw.w.localSGs, id)
	return nil
}

func (sw *securityGroupWriter) SetProjectID(_ context.Context, id, folderID string) (*kacho.SecurityGroupRecord, error) {
	if _, deleted := sw.w.deletedSGIDs[id]; deleted {
		return nil, repo.ErrNotFound
	}
	sg, ok := sw.w.localSGs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	sg.ProjectID = folderID
	cp := *sg
	return &cp, nil
}

// UsedBy — KAC-239 S2: потребители SG над working-set writer'а (G.2 — видит
// свои writes), parity с reader.
func (sw *securityGroupWriter) UsedBy(_ context.Context, sgID string) ([]domain.SecurityGroupReference, error) {
	return computeSGUsedBy(sgID, sw.w.localNIs, sw.w.local)
}

// UpdateRules / UpdateRule — упрощённая семантика (без xmin-OCC; mock не
// моделирует concurrent-conflict). Достаточно для unit-тестов use-case'ов.
func (sw *securityGroupWriter) UpdateRules(_ context.Context, sgID string, deleteIDs []string, add []domain.SecurityGroupRule) (*kacho.SecurityGroupRecord, error) {
	if _, deleted := sw.w.deletedSGIDs[sgID]; deleted {
		return nil, repo.ErrNotFound
	}
	sg, ok := sw.w.localSGs[sgID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	if len(deleteIDs) > 0 {
		toDel := make(map[string]struct{}, len(deleteIDs))
		for _, id := range deleteIDs {
			toDel[id] = struct{}{}
		}
		filtered := sg.Rules[:0]
		for _, r := range sg.Rules {
			if _, drop := toDel[r.ID]; drop {
				continue
			}
			filtered = append(filtered, r)
		}
		sg.Rules = filtered
	}
	sg.Rules = append(sg.Rules, add...)
	cp := *sg
	return &cp, nil
}

func (sw *securityGroupWriter) UpdateRule(_ context.Context, sgID, ruleID, description string, labels map[string]string, mask []string) (*kacho.SecurityGroupRecord, error) {
	if _, deleted := sw.w.deletedSGIDs[sgID]; deleted {
		return nil, repo.ErrNotFound
	}
	sg, ok := sw.w.localSGs[sgID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	applyMask := len(mask) > 0
	maskSet := map[string]struct{}{}
	for _, m := range mask {
		maskSet[m] = struct{}{}
	}
	found := false
	for i := range sg.Rules {
		if sg.Rules[i].ID != ruleID {
			continue
		}
		found = true
		if !applyMask {
			sg.Rules[i].Description = domain.RcDescription(description)
			sg.Rules[i].Labels = labels
		} else {
			if _, ok := maskSet["description"]; ok {
				sg.Rules[i].Description = domain.RcDescription(description)
			}
			if _, ok := maskSet["labels"]; ok {
				sg.Rules[i].Labels = labels
			}
		}
		break
	}
	if !found {
		return nil, repo.ErrNotFound
	}
	cp := *sg
	return &cp, nil
}

// Assertion: securityGroupReader/Writer implements iface.
var (
	_ kacho.SecurityGroupReaderIface = (*securityGroupReader)(nil)
	_ kacho.SecurityGroupWriterIface = (*securityGroupWriter)(nil)
)
