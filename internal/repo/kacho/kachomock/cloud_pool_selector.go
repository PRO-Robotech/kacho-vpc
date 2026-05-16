package kachomock

import (
	"context"
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// In-memory CloudPoolSelector reader/writer для kachomock. Wave 5 replicate
// (KAC-94 A.7 sub-PR 1/6).

// ---- CloudPoolSelector reader ----

type cloudPoolSelectorReader struct {
	snap map[string]*domain.CloudPoolSelector
}

func (r *cloudPoolSelectorReader) Get(_ context.Context, cloudID string) (*domain.CloudPoolSelector, error) {
	c, ok := r.snap[cloudID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

// ---- CloudPoolSelector writer ----

type cloudPoolSelectorWriter struct {
	w *writerImpl
}

func (cw *cloudPoolSelectorWriter) Get(_ context.Context, cloudID string) (*domain.CloudPoolSelector, error) {
	if _, deleted := cw.w.deletedCSIDs[cloudID]; deleted {
		return nil, repo.ErrNotFound
	}
	c, ok := cw.w.localCSs[cloudID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (cw *cloudPoolSelectorWriter) Set(_ context.Context, cloudID string, sel map[string]string, setBy string) error {
	if cw.w.deletedCSIDs != nil {
		delete(cw.w.deletedCSIDs, cloudID)
	}
	cw.w.localCSs[cloudID] = &domain.CloudPoolSelector{
		CloudID:  cloudID,
		Selector: sel,
		SetAt:    time.Now().UTC(),
		SetBy:    setBy,
	}
	return nil
}

func (cw *cloudPoolSelectorWriter) Unset(_ context.Context, cloudID string) error {
	if cw.w.deletedCSIDs == nil {
		cw.w.deletedCSIDs = make(map[string]struct{})
	}
	cw.w.deletedCSIDs[cloudID] = struct{}{}
	delete(cw.w.localCSs, cloudID)
	return nil
}

// Assertions.
var (
	_ kacho.CloudPoolSelectorReaderIface = (*cloudPoolSelectorReader)(nil)
	_ kacho.CloudPoolSelectorWriterIface = (*cloudPoolSelectorWriter)(nil)
)
