package precheck

import (
	"fmt"
	"sort"
)

// validateParams 按当时生效的结构约束校验参数；违反则返回 ErrInvalidParams。
func validateParams(schema *ParamSchema, params map[string]any) error {
	for _, key := range schema.Required {
		if _, ok := params[key]; !ok {
			return &ResolutionError{Kind: ErrInvalidParams, Detail: fmt.Sprintf("missing required param %q", key)}
		}
	}
	keys := make([]string, 0, len(schema.Types))
	for k := range schema.Types {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		v, ok := params[key]
		if !ok {
			continue
		}
		if !typeMatches(schema.Types[key], v) {
			return &ResolutionError{Kind: ErrInvalidParams, Detail: fmt.Sprintf("param %q must be %s", key, schema.Types[key])}
		}
	}
	return nil
}

func typeMatches(want string, v any) bool {
	switch want {
	case "string":
		_, ok := v.(string)
		return ok
	case "int":
		// JSON 解码数字统一取 float64，因此接受值为整数的 float64。
		switch n := v.(type) {
		case int:
			return true
		case int64:
			return true
		case float64:
			return n == float64(int64(n))
		}
		return false
	case "bool":
		_, ok := v.(bool)
		return ok
	case "number":
		switch v.(type) {
		case int, int64, float64:
			return true
		}
		return false
	default:
		return true
	}
}
