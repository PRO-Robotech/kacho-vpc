// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// SecurityGroupRecord — repo-проекция SecurityGroup: domain.SecurityGroup +
// CreatedAt (DB-managed). CreatedAt живет в repo-проекции, а не в domain, чтобы
// domain-сущность оставалась чистой бизнес-логикой без DB-managed полей.
//
// Use-case-слой читает *SecurityGroupRecord из репозитория (CQRS-iface
// SecurityGroupReaderIface / SecurityGroupWriterIface), а пишет в репо
// *domain.SecurityGroup (без CreatedAt — его проставляет БД).
type SecurityGroupRecord struct {
	domain.SecurityGroup
	CreatedAt time.Time
}
