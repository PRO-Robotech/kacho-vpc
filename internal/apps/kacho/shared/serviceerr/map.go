// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package serviceerr

import (
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// MapRepoErr — единая трансляция repo-sentinel в gRPC status.
//
// Sentinel-prefix (`failed precondition: `, `not found`, ...) удаляется при
// преобразовании в gRPC-сообщение, чтобы клиент видел чистый текст без
// internal-обертки.
//
// Fallthrough: неклассифицированный err (например raw pgx без обертки в repo)
// → codes.Internal с фиксированным "internal database error". Это закрывает
// info-leak через Operation.error.message, если новый repo-метод забыли обернуть.
//
// Resource-use-case'ы в `internal/apps/kacho/api/<x>/` держат собственные
// **локальные** копии `mapRepoErr`; этот общий `MapRepoErr` используется
// не-ресурсными сервисами в `internal/apps/kacho/services/*`
// (AddressPoolService, AddressReferenceService, NetworkInternal).
func MapRepoErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, stripSentinel(err, ErrNotFound))
	case errors.Is(err, ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, stripSentinel(err, ErrAlreadyExists))
	case errors.Is(err, ErrFailedPrecondition):
		return status.Error(codes.FailedPrecondition, stripSentinel(err, ErrFailedPrecondition))
	case errors.Is(err, ErrInvalidArg):
		return status.Error(codes.InvalidArgument, stripSentinel(err, ErrInvalidArg))
	case errors.Is(err, ErrInternal):
		return status.Error(codes.Internal, "internal database error")
	}
	// Если err уже gRPC-status (например из самого service-слоя через
	// status.Errorf) — пробрасываем как есть.
	// status.FromError возвращает (status, true) даже для не-status err
	// (с code=Unknown) — поэтому проверяем code != Unknown.
	if st, ok := status.FromError(err); ok && st.Code() != codes.Unknown {
		return err
	}
	// Defensive: raw error из repo без обертки → не leak'аем текст,
	// возвращаем generic Internal.
	return status.Error(codes.Internal, "internal database error")
}

// stripSentinel — извлекает «полезную» часть сообщения (после «sentinel: »),
// чтобы выдать клиенту чистый текст без internal-обертки sentinel-ошибки.
// Если err == sentinel или контекст не добавлен, возвращает sentinel.Error().
func stripSentinel(err, sentinel error) string {
	msg := err.Error()
	prefix := sentinel.Error() + ": "
	if rest, ok := strings.CutPrefix(msg, prefix); ok {
		return rest
	}
	return msg
}
