// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

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

// NICMacUniqueConstraint — имя UNIQUE-индекса network_interfaces_mac_address_key
// на network_interfaces.mac_address (baseline 0001_initial.sql). См. IsNICMacCollision.
const NICMacUniqueConstraint = "network_interfaces_mac_address_key"

// IsNICMacCollision — true если err — это нарушение UNIQUE на
// network_interfaces.mac_address (а не на (project_id, name) или другом
// constraint таблицы). Используется в networkInterfaceWriter.Insert
// (internal/repo/kacho/pg) чтобы различить retry-able MAC-collision от
// настоящего AlreadyExists по имени.
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

// NICUsedByIndexUniqueConstraint — имя partial-UNIQUE-индекса ni_used_by_index_uniq
// на (used_by_id, used_by_index) WHERE used_by_id<>” (миграция
// 0014_network_interface_used_by_index). См. IsNICIndexCollision.
const NICUsedByIndexUniqueConstraint = "ni_used_by_index_uniq"

// IsNICIndexCollision — true если err — это нарушение partial-UNIQUE на
// (used_by_id, used_by_index): выбранный слот уже занят другим NIC на том же
// инстансе. Используется networkInterfaceWriter.AttachToInstance, чтобы отличить
// retry-able slot-collision (auto-index пересчитывает свободный слот) от прочих
// нарушений. Аналог IsNICMacCollision.
func IsNICIndexCollision(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505" && pgErr.ConstraintName == NICUsedByIndexUniqueConstraint
	}
	return strings.Contains(err.Error(), NICUsedByIndexUniqueConstraint)
}

// IsFKViolation — Postgres foreign_key_violation (SQLSTATE 23503).
// Возникает на Delete parent с зависимыми child-row (RESTRICT FK).
// Маппится в gRPC FailedPrecondition ("Network is not empty").
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
// ("Subnet CIDRs can not overlap").
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
// массив v4_address_ids длиннее 1 на одном NIC). Маппится на gRPC
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

// resourceKindText маппит camelCase Go-имя ресурса в текст для error-message
// "invalid <kind> id 'X'": snake_case для многословных kind-ов (route_table),
// single-word для остального.
func resourceKindText(kind string) string {
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
// helpers-пакета. mapRepoErr в service-слое потом мапит ее на gRPC-status.
//
// НЕ leak'ает raw PG-сообщение клиенту: для неизвестных классов возвращает
// ErrInternal без exposing.
//
// kind/id — для AlreadyExists/NotFound сообщений (имя ресурса + id).
//
// SQLSTATE 22P02 (invalid_text_representation, malformed UUID-cast) →
// InvalidArgument: `invalid <kind> id '<id>'`.
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
		return fmt.Errorf("%w: invalid %s id '%s'", ErrInvalidArg, resourceKindText(kind), id)
	}
	if IsFKViolation(err) {
		return fmt.Errorf("%w: %s has dependent resources", ErrFailedPrecondition, kind)
	}
	if IsCheckViolation(err) {
		return fmt.Errorf("%w: %s violates check constraint", ErrInvalidArg, kind)
	}
	if IsExclusionViolation(err) {
		return fmt.Errorf("%w: value conflicts with existing %s", ErrFailedPrecondition, kind)
	}
	// Неклассифицированный класс (напр. 40001 serialization_failure, 40P01
	// deadlock, 57014 statement_timeout, connection reset). Клиент по контракту
	// получит фиксированный INTERNAL (serviceerr сворачивает ErrInternal-ветку в
	// "internal database error", no-leak), но root-cause сохраняем в цепочке для
	// server-side логов оператора — иначе SQLSTATE/constraint/detail теряются
	// безвозвратно на границе repo (CWE-778). Тот же `%w: %v`-паттерн, что и в
	// helpers/jsonb.go.
	return fmt.Errorf("%w: %v", ErrInternal, err)
}
