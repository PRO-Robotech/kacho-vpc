// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package address

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/types/known/anypb"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"

	// Blank-import регистрирует трансферы Address/time через init().
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// niReferrerType — ReferrerType в address_references для адресов, привязанных
// к NetworkInterface. Зеркальная копия константы из
// `internal/apps/kacho/api/networkinterface/create.go::niReferrerType`.
const niReferrerType = "network_interface"

// lbReferrerType — ReferrerType в address_references для VIP-адресов, привязанных
// к network load balancer'у (owner-сервис хранит referrer через SetReference).
const lbReferrerType = "network_load_balancer"

// referrerTypeLabel переводит машинный ReferrerType в человекочитаемую форму для
// Delete-guard-сообщения. Неизвестный тип отдается как есть.
func referrerTypeLabel(referrerType string) string {
	switch referrerType {
	case niReferrerType:
		return "network interface"
	case lbReferrerType:
		return "network_load_balancer"
	default:
		return referrerType
	}
}

// isUniqueViolation распознает UNIQUE-violation для retry-loop в allocate.
//
// Принципиальный путь: repo через wrapPgErr оборачивает SQLSTATE 23505 в
// ErrAlreadyExists — это и есть contract repo↔use-case. Substring-fallback
// оставлен для случаев когда какой-то новый repo может вернуть raw pgErr
// без обертки (defensive). Constraint-specific имена удалены — use-case не
// должен знать DB-schema.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, repo.ErrAlreadyExists) {
		return true
	}
	// Defensive fallback: общие признаки UNIQUE-violation без leak'а
	// constraint-имен в use-case.
	msg := err.Error()
	return strings.Contains(msg, "SQLSTATE 23505") ||
		strings.Contains(msg, "duplicate key value")
}

// marshalAddressRecord конвертирует repo-entity Address в *anypb.Any через
// DTO-реестр. Используется worker'ами Create/Update/Move для упаковки
// результата в Operation.response.
func marshalAddressRecord(rec *kachorepo.AddressRecord) (*anypb.Any, error) {
	var dst *vpcv1.Address
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer Address: %w", err)
	}
	return anypb.New(dst)
}
