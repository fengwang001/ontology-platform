package ontology

import "strings"

func coerceScalar(raw any, k Kind, mode Mode) (Outcome, error) {
	out := Outcome{Status: StatusPresent}
	var err error
	switch k {
	case String:
		out.String, err = toString(raw, mode, &out)
	case Int64:
		out.Int64, err = toInt64(raw, mode, &out)
	case Float64:
		out.Float64, err = toFloat64(raw, mode, &out)
	case Bool:
		out.Bool, err = toBool(raw, mode, &out)
	default:
		return out, newErr(CatTypeMismatch, "not a scalar target: %v", k)
	}
	if err != nil {
		return Outcome{Status: StatusPresent}, err
	}
	return out, nil
}

func toString(raw any, mode Mode, out *Outcome) (string, error) {
	switch v := raw.(type) {
	case string:
		return v, nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case int64:
		return formatInt(v), nil
	case int:
		return formatInt(int64(v)), nil
	case float64:
		return formatFloat(v), nil
	case float32:
		return formatFloat(float64(v)), nil
	default:
		err := newErr(CatTypeMismatch, "cannot coerce %s to String", typeName(raw))
		return "", settle(mode, err, newRecord(CatTypeMismatch, raw, "", ""), out)
	}
}

// toBool implements the closed acceptance set:
// true, false, 1, 0, 1.0, 0.0 and the strings
// "true","false","1","0" (case-insensitive, no trimming).
// Everything else (2, "yes", " true", "t") is CatInvalidBool.
func toBool(raw any, mode Mode, out *Outcome) (bool, error) {
	switch v := raw.(type) {
	case bool:
		return v, nil
	case int64:
		if v == 0 || v == 1 {
			return v == 1, nil
		}
	case int:
		if v == 0 || v == 1 {
			return v == 1, nil
		}
	case float64:
		if v == 0 || v == 1 {
			return v == 1, nil
		}
	case string:
		switch strings.ToLower(v) {
		case "true", "1":
			return true, nil
		case "false", "0":
			return false, nil
		}
		err := newErr(CatInvalidBool, "string %s is not true/false/1/0 (no trimming allowed)", valueString(v))
		return false, settle(mode, err, newRecord(CatInvalidBool, raw, false, "whitespace is significant"), out)
	default:
		err := newErr(CatTypeMismatch, "cannot coerce %s to Bool", typeName(raw))
		return false, settle(mode, err, newRecord(CatTypeMismatch, raw, false, ""), out)
	}
	err := newErr(CatInvalidBool, "value %s is outside the closed boolean set {true,false,1,0}", valueString(raw))
	return false, settle(mode, err, newRecord(CatInvalidBool, raw, false, ""), out)
}

func typeName(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case int64, int:
		return "integer"
	case float64, float32:
		return "float"
	default:
		return goTypeName(v)
	}
}
