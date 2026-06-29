// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// SubnetRecord — repo-проекция Subnet: domain.Subnet + CreatedAt (DB-managed).
// Проекция живет рядом с repo-имплементацией, а не в domain. CreatedAt
// проставляется в SubnetRepo.Insert (UTC-now); source of truth — БД.
//
// Use-case-слой читает *SubnetRecord из репозитория (порт SubnetRepo /
// SubnetReaderIface / SubnetWriterIface) и пробрасывает в DTO/handler. В proto
// клиенту уходит CreatedAt из этой структуры (truncate до секунд, см.
// dto/type2pb/time.go).
type SubnetRecord struct {
	domain.Subnet
	CreatedAt time.Time
}
