package repo

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// uuidToStr конвертирует pgtype.UUID в строку.
func uuidToStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// strToUUID парсит строку UUID в pgtype.UUID.
func strToUUID(s string) (pgtype.UUID, error) {
	if s == "" {
		return pgtype.UUID{}, nil
	}
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	return u, nil
}

// tsToTime конвертирует pgtype.Timestamptz в time.Time.
func tsToTime(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}

// tsToTimePtr конвертирует pgtype.Timestamptz в *time.Time (nil если невалидный).
func tsToTimePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

// mapToJSON сериализует map[string]string в JSON bytes.
func mapToJSON(m map[string]string) []byte {
	if m == nil {
		return []byte("{}")
	}
	b, _ := json.Marshal(m)
	return b
}

// jsonToMap десериализует JSON bytes в map[string]string.
func jsonToMap(b []byte) map[string]string {
	if len(b) == 0 {
		return map[string]string{}
	}
	var m map[string]string
	_ = json.Unmarshal(b, &m)
	if m == nil {
		return map[string]string{}
	}
	return m
}
