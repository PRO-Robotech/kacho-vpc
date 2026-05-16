package handler

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
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
	case errors.Is(err, repo.ErrNotFound):
		return status.Error(codes.NotFound, repo.ErrNotFound.Error())
	case errors.Is(err, repo.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, repo.ErrAlreadyExists.Error())
	case errors.Is(err, repo.ErrFailedPrecondition):
		return status.Error(codes.FailedPrecondition, repo.ErrFailedPrecondition.Error())
	case errors.Is(err, repo.ErrPoolNotResolved):
		// FINDING-008: ни один шаг IPAM cascade не дал pool — это FailedPrecondition
		// (конфигурация пулов неполна), а не INTERNAL. Без leak'а raw-текста.
		return status.Error(codes.FailedPrecondition, repo.ErrPoolNotResolved.Error())
	case errors.Is(err, repo.ErrInvalidArg):
		return status.Error(codes.InvalidArgument, repo.ErrInvalidArg.Error())
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
