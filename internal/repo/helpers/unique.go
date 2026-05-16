package helpers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// IsUniqueViolation — Postgres unique-constraint violation (SQLSTATE 23505).
// Используется в Create/Update для маппинга в gRPC AlreadyExists.
func IsUniqueViolation(err error) bool {
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

// NICMacUniqueConstraint — имя UNIQUE-индекса на network_interfaces.mac_address
// (миграция 0014). См. IsNICMacCollision.
const NICMacUniqueConstraint = "network_interfaces_mac_address_key"

// IsNICMacCollision — true если err — это нарушение UNIQUE на
// network_interfaces.mac_address (а не на (folder_id, name) или другом
// constraint таблицы). Используется в NetworkInterfaceRepo.Insert чтобы
// различить retry-able MAC-collision от настоящего AlreadyExists по имени.
func IsNICMacCollision(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505" && pgErr.ConstraintName == NICMacUniqueConstraint
	}
	return strings.Contains(err.Error(), NICMacUniqueConstraint)
}

// IsFKViolation — Postgres foreign_key_violation (SQLSTATE 23503).
// Возникает на Delete parent с зависимыми child-row (RESTRICT FK).
// Маппится в gRPC FailedPrecondition (как у YC: "Network is not empty").
func IsFKViolation(err error) bool {
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

// IsExclusionViolation — PG SQLSTATE 23P01 (exclusion_violation), возникает
// при нарушении EXCLUDE constraint (например `subnets_no_overlap_v4` —
// пересекающиеся v4 CIDR в одной VPC). Маппится на gRPC FailedPrecondition
// (verbatim YC: code:9 "Subnet CIDRs can not overlap").
func IsExclusionViolation(err error) bool {
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

// IsCheckViolation — PG SQLSTATE 23514 (check_violation). Возникает при
// нарушении CHECK constraint (например, `network_interfaces_v4_addr_max1` —
// массив v4_address_ids длиннее 1 на одном NIC, KAC-55). Маппится на gRPC
// InvalidArgument через WrapPgErr.
func IsCheckViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23514"
	}
	s := err.Error()
	return strings.Contains(s, "23514") || strings.Contains(s, "check constraint")
}

// YCKindText маппит camelCase Go-имя ресурса в YC verbatim text для
// error-message "invalid <kind> id 'X'". YC использует snake_case для
// многословных kind-ов (route_table), single-word для остального.
func YCKindText(kind string) string {
	switch kind {
	case "RouteTable":
		return "route_table"
	}
	return strings.ToLower(kind)
}

// IsInvalidUUID — PG SQLSTATE 22P02 (invalid_text_representation),
// возникает когда в WHERE id=$1 передан non-UUID string.
func IsInvalidUUID(err error) bool {
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

// WrapPgErr классифицирует pgx-ошибку и возвращает sentinel-ошибку из
// helpers-пакета. mapRepoErr в service-слое потом мапит её на gRPC-status.
//
// НЕ leak'ает raw PG-сообщение клиенту: для неизвестных классов возвращает
// ErrInternal без exposing.
//
// kind/id — для AlreadyExists/NotFound сообщений (имя ресурса + id).
//
// SQLSTATE 22P02 (invalid_text_representation, malformed UUID-cast) → verbatim
// YC InvalidArgument: `invalid <kind> id '<id>'`.
func WrapPgErr(err error, kind, id string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if id != "" {
			return fmt.Errorf("%w: %s %s not found", ErrNotFound, kind, id)
		}
		return ErrNotFound
	}
	if IsUniqueViolation(err) {
		return ErrAlreadyExists
	}
	if IsInvalidUUID(err) {
		return fmt.Errorf("%w: invalid %s id '%s'", ErrInvalidArg, YCKindText(kind), id)
	}
	if IsFKViolation(err) {
		return fmt.Errorf("%w: %s has dependent resources", ErrFailedPrecondition, kind)
	}
	if IsCheckViolation(err) {
		return fmt.Errorf("%w: %s violates check constraint", ErrInvalidArg, kind)
	}
	if IsExclusionViolation(err) {
		return fmt.Errorf("%w: value conflicts with existing %s", ErrInvalidArg, kind)
	}
	return ErrInternal
}
