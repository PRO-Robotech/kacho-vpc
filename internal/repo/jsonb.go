package repo

import "github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"

// KAC-94 A.7 sub-PR 4/6: реальная реализация — в `internal/repo/helpers/jsonb.go`.
// Тонкие unexported алиасы оставлены для legacy `*_repo.go`, которые будут
// удалены в Sub-PR 6.

// marshalJSONB — alias на helpers.MarshalJSONB.
func marshalJSONB(v any, field string) ([]byte, error) {
	return helpers.MarshalJSONB(v, field)
}

// unmarshalJSONB — alias на helpers.UnmarshalJSONB.
func unmarshalJSONB(raw []byte, target any, field string) error {
	return helpers.UnmarshalJSONB(raw, target, field)
}
