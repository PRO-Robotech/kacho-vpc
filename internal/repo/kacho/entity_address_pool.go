// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import "github.com/PRO-Robotech/kacho-vpc/internal/domain"

// AddressPoolRecord — repo-entity для AddressPool; единый Record-pattern с
// kacho.NetworkRecord / kacho.AddressRecord / kacho.SubnetRecord для всех
// ресурсов VPC.
//
// CreatedAt/ModifiedAt физически живут в domain.AddressPool: Record embed'ит
// domain, поля доступны через promoted-fields (`rec.CreatedAt`), и pg-impl
// читает их по этому же пути.
type AddressPoolRecord struct {
	domain.AddressPool
}
