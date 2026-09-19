package ontology

import (
	"math"
	"reflect"
)

// evalCompare evaluates one comparison leaf whose property value is
// present in the entity. Missing properties are handled by the caller.
//
// Returns Unknown (with nil error) when a float64 operand is NaN, since
// NaN comparisons are indeterminate rather than type errors.
func evalCompare(property string, value any, op Op, literal any) (Tri, error) {
	if isNaN(value) || isNaN(literal) {
		return Unknown, nil
	}

	vk := reflect.TypeOf(value).Kind()
	lk := reflect.TypeOf(literal).Kind()

	switch {
	case isNumber(vk) && isNumber(lk):
		return compareNumbers(property, toFloat(value), toFloat(literal), op)
	case vk == reflect.Bool && lk == reflect.Bool:
		return compareBools(property, value.(bool), literal.(bool), op)
	case vk == reflect.String && lk == reflect.String:
		return compareStrings(property, value.(string), literal.(string), op)
	default:
		return Unknown, &TypeError{
			Property:  property,
			Op:        op,
			LeftType:  typeName(value),
			RightType: typeName(literal),
			Reason:    "incomparable types",
		}
	}
}

func compareNumbers(property string, left, right float64, op Op) (Tri, error) {
	switch op {
	case Eq:
		return Bool(left == right), nil
	case Lt:
		return Bool(left < right), nil
	case Gt:
		return Bool(left > right), nil
	default:
		return Unknown, &TypeError{Property: property, Op: op, Reason: "unknown operator"}
	}
}

func compareBools(property string, left, right bool, op Op) (Tri, error) {
	if op != Eq {
		return Unknown, &TypeError{
			Property:  property,
			Op:        op,
			LeftType:  "bool",
			RightType: "bool",
			Reason:    "bool only supports eq",
		}
	}
	return Bool(left == right), nil
}

func compareStrings(property, left, right string, op Op) (Tri, error) {
	switch op {
	case Eq:
		return Bool(left == right), nil
	case Lt:
		return Bool(left < right), nil
	case Gt:
		return Bool(left > right), nil
	default:
		return Unknown, &TypeError{Property: property, Op: op, Reason: "unknown operator"}
	}
}

func isNumber(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func toFloat(v any) float64 {
	return reflect.ValueOf(v).Convert(float64Type).Float()
}

func isNaN(v any) bool {
	if f, ok := v.(float64); ok {
		return math.IsNaN(f)
	}
	if f, ok := v.(float32); ok {
		return math.IsNaN(float64(f))
	}
	return false
}
