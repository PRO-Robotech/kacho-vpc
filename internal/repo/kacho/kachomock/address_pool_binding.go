package kachomock

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// In-memory binding (network_default / address_override) reader/writer для
// kachomock. Wave 5 replicate (KAC-94 A.7 sub-PR 1/6).

// ---- AddressPoolBinding reader ----

type addressPoolBindingReader struct {
	netDef   map[string]string // network_id → pool_id
	addrOver map[string]string // address_id → pool_id
}

// GetNetworkDefault возвращает pool-id, привязанный к сети как default (ErrNotFound если нет).
func (r *addressPoolBindingReader) GetNetworkDefault(_ context.Context, networkID string) (string, error) {
	p, ok := r.netDef[networkID]
	if !ok {
		return "", repo.ErrNotFound
	}
	return p, nil
}

// GetAddressOverride возвращает pool-id, override-привязанный к адресу (ErrNotFound если нет).
func (r *addressPoolBindingReader) GetAddressOverride(_ context.Context, addressID string) (string, error) {
	p, ok := r.addrOver[addressID]
	if !ok {
		return "", repo.ErrNotFound
	}
	return p, nil
}

// ---- AddressPoolBinding writer ----

type addressPoolBindingWriter struct {
	w *writerImpl
}

// GetNetworkDefault возвращает network-default pool-id из writer-локального стора.
func (bw *addressPoolBindingWriter) GetNetworkDefault(_ context.Context, networkID string) (string, error) {
	if _, deleted := bw.w.deletedNDIDs[networkID]; deleted {
		return "", repo.ErrNotFound
	}
	p, ok := bw.w.localNDs[networkID]
	if !ok {
		return "", repo.ErrNotFound
	}
	return p, nil
}

// GetAddressOverride возвращает address-override pool-id из writer-локального стора.
func (bw *addressPoolBindingWriter) GetAddressOverride(_ context.Context, addressID string) (string, error) {
	if _, deleted := bw.w.deletedAOIDs[addressID]; deleted {
		return "", repo.ErrNotFound
	}
	p, ok := bw.w.localAOs[addressID]
	if !ok {
		return "", repo.ErrNotFound
	}
	return p, nil
}

// SetNetworkDefault привязывает pool к сети как default в writer-локальном сторе.
func (bw *addressPoolBindingWriter) SetNetworkDefault(_ context.Context, networkID, poolID string) error {
	if bw.w.deletedNDIDs != nil {
		delete(bw.w.deletedNDIDs, networkID)
	}
	bw.w.localNDs[networkID] = poolID
	return nil
}

// UnsetNetworkDefault снимает network-default-привязку в writer-локальном сторе.
func (bw *addressPoolBindingWriter) UnsetNetworkDefault(_ context.Context, networkID string) error {
	if bw.w.deletedNDIDs == nil {
		bw.w.deletedNDIDs = make(map[string]struct{})
	}
	bw.w.deletedNDIDs[networkID] = struct{}{}
	delete(bw.w.localNDs, networkID)
	return nil
}

// SetAddressOverride привязывает pool к адресу как override в writer-локальном сторе.
func (bw *addressPoolBindingWriter) SetAddressOverride(_ context.Context, addressID, poolID string) error {
	if bw.w.deletedAOIDs != nil {
		delete(bw.w.deletedAOIDs, addressID)
	}
	bw.w.localAOs[addressID] = poolID
	return nil
}

// UnsetAddressOverride снимает address-override-привязку в writer-локальном сторе.
func (bw *addressPoolBindingWriter) UnsetAddressOverride(_ context.Context, addressID string) error {
	if bw.w.deletedAOIDs == nil {
		bw.w.deletedAOIDs = make(map[string]struct{})
	}
	bw.w.deletedAOIDs[addressID] = struct{}{}
	delete(bw.w.localAOs, addressID)
	return nil
}

// Assertions.
var (
	_ kacho.AddressPoolBindingReaderIface = (*addressPoolBindingReader)(nil)
	_ kacho.AddressPoolBindingWriterIface = (*addressPoolBindingWriter)(nil)
)
