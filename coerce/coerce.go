package coerce

import (
	"errors"
	"math"
	"strconv"
)

var ErrTypeConflict = errors.New("type conflict")

type Type string

const (
	Long    Type = "long"
	Double  Type = "double"
	Keyword Type = "keyword"
	Bool    Type = "bool"
	Object  Type = "object"
)

func Infer(v any) (Type, bool) {
	switch v.(type) {
	case bool:
		return Bool, true
	case int64:
		return Long, true
	case float64:
		return Double, true
	case string:
		return Keyword, true
	case map[string]any:
		return Object, true
	default:
		return "", false
	}
}

func Coerce(t Type, v any) (any, error) {
	switch t {
	case Long:
		return coerceLong(v)
	case Double:
		return coerceDouble(v)
	case Keyword:
		return coerceKeyword(v)
	case Bool:
		return coerceBool(v)
	case Object:
		if _, ok := v.(map[string]any); ok {
			return v, nil
		}
		return nil, ErrTypeConflict
	default:
		return nil, ErrTypeConflict
	}
}

func coerceLong(v any) (any, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, ErrTypeConflict
		}
		if x != math.Trunc(x) {
			return nil, ErrTypeConflict
		}
		if x < float64(math.MinInt64) || x > float64(math.MaxInt64) {
			return nil, ErrTypeConflict
		}
		return int64(x), nil
	case string:
		return parseCanonicalInt(x)
	default:
		return nil, ErrTypeConflict
	}
}

func parseCanonicalInt(s string) (int64, error) {
	if s == "" {
		return 0, ErrTypeConflict
	}
	neg := false
	digits := s
	if s[0] == '-' {
		neg = true
		digits = s[1:]
	} else if s[0] == '+' {
		return 0, ErrTypeConflict
	}
	if digits == "" || (neg && digits == "0") {
		return 0, ErrTypeConflict
	}
	if len(digits) > 1 && digits[0] == '0' {
		return 0, ErrTypeConflict
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, ErrTypeConflict
		}
	}
	n, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return 0, ErrTypeConflict
	}
	if neg {
		if n > uint64(math.MaxInt64)+1 {
			return 0, ErrTypeConflict
		}
		return -int64(n), nil
	}
	if n > math.MaxInt64 {
		return 0, ErrTypeConflict
	}
	return int64(n), nil
}

func coerceDouble(v any) (any, error) {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, ErrTypeConflict
		}
		return x, nil
	case int64:
		const limit = 1 << 53
		if x < -limit || x > limit {
			return nil, ErrTypeConflict
		}
		return float64(x), nil
	default:
		return nil, ErrTypeConflict
	}
}

func coerceKeyword(v any) (any, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case bool:
		return strconv.FormatBool(x), nil
	default:
		return nil, ErrTypeConflict
	}
}

func coerceBool(v any) (any, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		if x == "true" {
			return true, nil
		}
		if x == "false" {
			return false, nil
		}
		return nil, ErrTypeConflict
	default:
		return nil, ErrTypeConflict
	}
}
