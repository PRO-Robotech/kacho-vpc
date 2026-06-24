// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kachomock

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// In-memory binding (network_default) reader/writer для kachomock.

// ---- AddressPoolBinding reader ----

type addressPoolBindingReader struct {
	netDef map[string]string // network_id → pool_id
}

func (r *addressPoolBindingReader) GetNetworkDefault(_ context.Context, networkID string) (string, error) {
	p, ok := r.netDef[networkID]
	if !ok {
		return "", repo.ErrNotFound
	}
	return p, nil
}

// ---- AddressPoolBinding writer ----

type addressPoolBindingWriter struct {
	w *writerImpl
}

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

func (bw *addressPoolBindingWriter) SetNetworkDefault(_ context.Context, networkID, poolID string) error {
	if bw.w.deletedNDIDs != nil {
		delete(bw.w.deletedNDIDs, networkID)
	}
	bw.w.localNDs[networkID] = poolID
	return nil
}

func (bw *addressPoolBindingWriter) UnsetNetworkDefault(_ context.Context, networkID string) error {
	if bw.w.deletedNDIDs == nil {
		bw.w.deletedNDIDs = make(map[string]struct{})
	}
	bw.w.deletedNDIDs[networkID] = struct{}{}
	delete(bw.w.localNDs, networkID)
	return nil
}

// Compile-time проверка соответствия интерфейсам.
var (
	_ kacho.AddressPoolBindingReaderIface = (*addressPoolBindingReader)(nil)
	_ kacho.AddressPoolBindingWriterIface = (*addressPoolBindingWriter)(nil)
)
