// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package handler

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// internalMapErr — error-mapper для admin/Internal-handler'ов.
//
// Гарантирует, что raw pgx-text (несет hostname/db/query fragment) не утекает в
// response даже на cluster-internal listener (:9091) — защита от info-leak при
// ослаблении изоляции :9091 (admin-tooling, port-forward, lateral movement из
// соседнего pod). Sentinel service-errors классифицируются по коду; raw pgErr →
// generic Internal без leak'а. Вызывается как `return internalMapErr(tag, err)`.
//
// Для sentinel-branch'ей возвращаем sentinel.Error() без wrap-tail: иначе
// `err.Error()` от обернутого `fmt.Errorf("...: %w: row %v", ErrNotFound, pgErr.Detail)`
// протащил бы pgx-detail наружу через NotFound branch.
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
		// Ни один шаг IPAM cascade не дал pool — это FailedPrecondition
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
	// Defensive: raw err — без leak'а текста. Tag дает оператору запрос-ID
	// без доступа к pgx-internal info.
	if tag == "" {
		tag = "internal error"
	}
	return status.Error(codes.Internal, tag)
}
