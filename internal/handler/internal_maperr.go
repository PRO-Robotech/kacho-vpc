package handler

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// internalMapErr — admin/Internal-handler error mapper.
//
// Назначение: гарантировать что raw pgx-text (хранит hostname/db/query
// fragment) не уходит в response даже на cluster-internal listener (:9091).
// До добавления этого helper'а Internal handlers (internal_address_handler,
// internal_watch_handler) шли по pattern'у `status.Errorf(codes.Internal,
// "begin tx: %v", err)` — info-leak vector в случае ослабления изоляции
// :9091 (admin-tooling, port-forward, lateral movement из соседнего pod).
//
// Используется handler'ами как `return internalMapErr(ctx-tag, err)`. Sentinel
// service-errors классифицируются; raw pgErr → generic Internal без leak'а.
//
// R8 fix M1: для sentinel branch'ей возвращаем sentinel.Error() без wrap-tail.
// Иначе `fmt.Errorf("get address: %w: row %v", ErrNotFound, pgErr.Detail)` →
// `err.Error()` отдаст «get address: not found: row {hostname=db-1,...}» →
// pgx-leak через NotFound branch.
func internalMapErr(tag string, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, service.ErrNotFound):
		return status.Error(codes.NotFound, service.ErrNotFound.Error())
	case errors.Is(err, service.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, service.ErrAlreadyExists.Error())
	case errors.Is(err, service.ErrFailedPrecondition):
		return status.Error(codes.FailedPrecondition, service.ErrFailedPrecondition.Error())
	case errors.Is(err, service.ErrInvalidArg):
		return status.Error(codes.InvalidArgument, service.ErrInvalidArg.Error())
	}
	// Уже-сформированный gRPC status (не Unknown) пробрасываем — например
	// status.Error из самого service-слоя.
	if st, ok := status.FromError(err); ok && st.Code() != codes.Unknown {
		return err
	}
	// Defensive: raw err — без leak'а текста. Tag даёт оператору запрос-ID
	// без доступа к pgx-internal info.
	if tag == "" {
		tag = "internal error"
	}
	return status.Error(codes.Internal, tag)
}
