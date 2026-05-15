package repo

import (
	"encoding/json"
	"fmt"

	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// marshalJSONB сериализует v в JSONB-байты для записи в БД. Возвращает обёрнутую
// ports.ErrInternal при ошибке (json.Marshal failure).
//
// Для domain-типов VPC (map[string]string labels, []domain.SecurityGroupRule,
// *domain.DhcpOptions, []domain.StaticRoute, ExternalIpv4Spec, InternalIpv4Spec,
// *domain.DnsOptions) json.Marshal на практике не возвращает ошибку — типы
// содержат только stdlib-типы без channel/func/cyclic-ref. Но мы всё равно
// пробрасываем ошибку наверх (а не паникуем, см. TODO #24): если в будущем
// добавится тип, который теоретически может marshal-fail (например *anypb.Any с
// cyclic proto), сбой превращается в обычную INTERNAL-ошибку repo-метода, а не
// в panic. Это парная форма к unmarshalJSONB.
func marshalJSONB(v any, field string) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal JSONB %s: %v", ports.ErrInternal, field, err)
	}
	return b, nil
}

// unmarshalJSONB десериализует JSONB-байты из БД в target. Возвращает обёрнутую
// ports.ErrInternal при ошибке (повреждённый payload, schema mismatch).
//
// Заменяет ранее использованный silent `_ = json.Unmarshal(...)` (TODO #23).
// nil/empty raw — no-op (target остаётся zero-value).
func unmarshalJSONB(raw []byte, target any, field string) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("%w: corrupted JSONB %s: %v", ports.ErrInternal, field, err)
	}
	return nil
}
