package service

// normalizeMap нормализует nil-map к пустой map для корректного сравнения.
func normalizeMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
