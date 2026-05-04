package repo

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// invalidPageTokenErr оборачивает ошибку decodePageToken в gRPC InvalidArgument.
// Не утечь raw repo-error клиенту (PAGE-TOKEN-LEAK finding) — page_token это
// клиентский input, а не domain-state.
func invalidPageTokenErr(err error) error {
	return status.Errorf(codes.InvalidArgument, "page_token is invalid: %v", err)
}

// encodePageToken кодирует created_at + id в непрозрачный page_token.
func encodePageToken(createdAt time.Time, id string) string {
	raw := strconv.FormatInt(createdAt.UnixNano(), 10) + ":" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodePageToken декодирует page_token обратно в (created_at, id).
func decodePageToken(token string) (time.Time, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return time.Time{}, "", err
	}
	parts := strings.SplitN(string(b), ":", 2)
	if len(parts) != 2 {
		return time.Time{}, "", errors.New("malformed token")
	}
	ns, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, "", err
	}
	return time.Unix(0, ns).UTC(), parts[1], nil
}
