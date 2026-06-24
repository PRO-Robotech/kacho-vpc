// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package helpers

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// InvalidPageTokenErr оборачивает ошибку DecodePageToken в gRPC InvalidArgument.
// Raw repo-error наружу не утекает — page_token это клиентский input, а не
// domain-state.
func InvalidPageTokenErr(err error) error {
	return status.Errorf(codes.InvalidArgument, "page_token is invalid: %v", err)
}

// InvalidFilterErr оборачивает ParseError из filter.Parse в gRPC InvalidArgument
// со стабильным message ("Bad expression at column N. ...").
func InvalidFilterErr(err error) error {
	return status.Error(codes.InvalidArgument, err.Error())
}

// EncodePageToken кодирует created_at + id в непрозрачный page_token.
func EncodePageToken(createdAt time.Time, id string) string {
	raw := strconv.FormatInt(createdAt.UnixNano(), 10) + ":" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodePageToken декодирует page_token обратно в (created_at, id).
func DecodePageToken(token string) (time.Time, string, error) {
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
