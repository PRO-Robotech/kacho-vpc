// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package helpers

import (
	"encoding/json"
	"fmt"
)

// MarshalJSONB сериализует v в JSONB-байты для записи в БД. Возвращает обернутую
// ErrInternal при ошибке (json.Marshal failure).
//
// Для domain-типов VPC (map[string]string labels, []domain.SecurityGroupRule,
// *domain.DhcpOptions, []domain.StaticRoute, ExternalIpv4Spec, InternalIpv4Spec,
// *domain.DnsOptions) json.Marshal на практике не возвращает ошибку — типы
// содержат только stdlib-типы без channel/func/cyclic-ref. Но мы все равно
// пробрасываем ошибку наверх (а не паникуем): если в будущем добавится тип,
// который теоретически может marshal-fail, сбой превращается в обычную
// INTERNAL-ошибку repo-метода, а не в panic.
func MarshalJSONB(v any, field string) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal JSONB %s: %v", ErrInternal, field, err)
	}
	return b, nil
}

// UnmarshalJSONB десериализует JSONB-байты из БД в target. Возвращает обернутую
// ErrInternal при ошибке (поврежденный payload, schema mismatch).
//
// nil/empty raw — no-op (target остается zero-value).
func UnmarshalJSONB(raw []byte, target any, field string) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("%w: corrupted JSONB %s: %v", ErrInternal, field, err)
	}
	return nil
}
