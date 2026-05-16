package repo

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
)

// KAC-94 A.7 sub-PR 4/6: реальная реализация — в `internal/repo/helpers/paging.go`.
// Тонкие unexported алиасы оставлены для legacy `*_repo.go`, которые будут
// удалены в Sub-PR 6.

// invalidPageTokenErr — alias на helpers.InvalidPageTokenErr.
func invalidPageTokenErr(err error) error {
	return helpers.InvalidPageTokenErr(err)
}

// invalidFilterErr — alias на helpers.InvalidFilterErr.
func invalidFilterErr(err error) error {
	return helpers.InvalidFilterErr(err)
}

// encodePageToken — alias на helpers.EncodePageToken.
func encodePageToken(createdAt time.Time, id string) string {
	return helpers.EncodePageToken(createdAt, id)
}

// decodePageToken — alias на helpers.DecodePageToken.
func decodePageToken(token string) (time.Time, string, error) {
	return helpers.DecodePageToken(token)
}
