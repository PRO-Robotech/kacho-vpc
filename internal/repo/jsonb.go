package repo

import (
	"encoding/json"
	"fmt"

	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// mustMarshalJSON сериализует v в JSON, паникует при ошибке.
//
// Для domain-типов VPC (map[string]string labels, []domain.SecurityGroupRule,
// *domain.DhcpOptions, []domain.StaticRoute, ExternalIpv4Spec, InternalIpv4Spec,
// *domain.DnsOptions) json.Marshal не может вернуть ошибку — типы содержат только
// stdlib-типы без channel/func/cyclic-ref. Если ошибка всё-таки возникнет — это
// invariant violation в domain, не recoverable в repo-layer.
//
// Это явное "fail loud" вместо silent `_` (см. TODO #11). Если в будущем добавим
// тип, который теоретически может marshal-fail (например *anypb.Any с cyclic
// proto), переделаем на error-returning форму через marshalJSONB.
func mustMarshalJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("repo: json.Marshal failed for %T: %v (invariant violation)", v, err))
	}
	return b
}

// unmarshalJSONB десериализует JSONB-байты из БД в target. Возвращает обёрнутую
// service.ErrInternal при ошибке (повреждённый payload, schema mismatch).
//
// Заменяет ранее использованный silent `_ = json.Unmarshal(...)` (TODO #23).
// nil/empty raw — no-op (target остаётся zero-value).
func unmarshalJSONB(raw []byte, target any, field string) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("%w: corrupted JSONB %s: %v", service.ErrInternal, field, err)
	}
	return nil
}
