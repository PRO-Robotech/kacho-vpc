package repo

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// isUniqueViolation возвращает true если err — Postgres unique-constraint violation (SQLSTATE 23505).
// Используется в Create/Update для маппинга в gRPC AlreadyExists.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	s := err.Error()
	return strings.Contains(s, "23505") || strings.Contains(s, "duplicate key value")
}

// isFKViolation — Postgres foreign_key_violation (SQLSTATE 23503).
// Возникает на Delete parent с зависимыми child-row (RESTRICT FK).
// Маппится в gRPC FailedPrecondition (как у YC: "Network is not empty").
func isFKViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	s := err.Error()
	return strings.Contains(s, "23503") || strings.Contains(s, "violates foreign key")
}

// isExclusionViolation — PG SQLSTATE 23P01 (exclusion_violation), возникает
// при нарушении EXCLUDE constraint (например `subnets_no_overlap_v4` —
// пересекающиеся v4 CIDR в одной VPC). Маппится на gRPC FailedPrecondition
// (verbatim YC: code:9 "Subnet CIDRs can not overlap").
func isExclusionViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23P01"
	}
	s := err.Error()
	return strings.Contains(s, "23P01") || strings.Contains(s, "exclusion constraint")
}

// ycKindText маппит camelCase Go-имя ресурса в YC verbatim text для
// error-message "invalid <kind> id 'X'". YC использует snake_case для
// многословных kind-ов (route_table), single-word для остального.
func ycKindText(kind string) string {
	switch kind {
	case "RouteTable":
		return "route_table"
	}
	return strings.ToLower(kind)
}

// isInvalidUUID — PG SQLSTATE 22P02 (invalid_text_representation),
// возникает когда в WHERE id=$1 передан non-UUID string.
func isInvalidUUID(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "22P02"
	}
	s := err.Error()
	return strings.Contains(s, "22P02") || strings.Contains(s, "invalid input syntax for type uuid")
}

// wrapPgErr классифицирует pgx-ошибку и возвращает sentinel-ошибку из
// service-пакета. mapRepoErr в service-слое потом мапит её на gRPC-status.
//
// НЕ leak'ает raw PG-сообщение клиенту: для неизвестных классов возвращает
// nil-маркер и caller должен сам обернуть как Internal без exposing.
//
// kind/id — для AlreadyExists/NotFound сообщений (имя ресурса + id).
//
// SQLSTATE 22P02 (invalid_text_representation, malformed UUID-cast) → verbatim
// YC InvalidArgument: `invalid <kind> id '<id>'`. См. YC-DIFF-GET-NONEXISTENT-CODE.md.
func wrapPgErr(err error, kind, id string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// Verbatim YC text для well-formed-but-absent: "<Kind> <id> not found".
		if id != "" {
			return fmt.Errorf("%w: %s %s not found", service.ErrNotFound, kind, id)
		}
		return service.ErrNotFound
	}
	if isUniqueViolation(err) {
		return service.ErrAlreadyExists
	}
	if isInvalidUUID(err) {
		// Verbatim YC text: "invalid <kind> id '<id>'", code:3 InvalidArgument.
		// YC сам валидирует id-формат в gateway-слое; Kachō ловит на DB-уровне
		// через 22P02 и возвращает то же сообщение. Маппинг kind → YC text:
		//   Network → "network", Subnet → "subnet", Address → "address",
		//   RouteTable → "route_table" (snake_case как у YC).
		return fmt.Errorf("%w: invalid %s id '%s'", service.ErrInvalidArg, ycKindText(kind), id)
	}
	if isFKViolation(err) {
		return fmt.Errorf("%w: %s has dependent resources", service.ErrFailedPrecondition, kind)
	}
	if isExclusionViolation(err) {
		return fmt.Errorf("%w: value conflicts with existing %s", service.ErrInvalidArg, kind)
	}
	return service.ErrInternal
}
