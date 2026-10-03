package mirror

import (
	"strings"

	"ontology/diff"
)

// normalizeHeaders 校验头名非空、无大小写不同的同名头，并返回全小写副本。
func normalizeHeaders(headers map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(headers)+1)
	for name, val := range headers {
		if name == "" {
			return nil, ErrInvalid
		}
		lower := strings.ToLower(name)
		if _, exists := out[lower]; exists {
			return nil, ErrInvalid // 大小写不同的同名头
		}
		out[lower] = val
	}
	return out, nil
}

// stripHeaders 返回去掉敏感头后的新映射，不修改入参。
func stripHeaders(headers map[string]string, sensitive map[string]struct{}) map[string]string {
	out := make(map[string]string, len(headers)+1)
	for name, val := range headers {
		if _, drop := sensitive[name]; drop {
			continue
		}
		out[name] = val
	}
	return out
}

func validResponse(r diff.Response) bool {
	if r.Status < 0 || r.Status > 1_000_000_000 {
		return false
	}
	for name := range r.Fields {
		if name == "" {
			return false
		}
	}
	return true
}
