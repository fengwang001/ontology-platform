package coercion

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	minInt64Float = float64(math.MinInt64)
	maxInt64Float = float64(math.MaxInt64)
)

func (c *context) convertScalar(value any, kind Kind) any {
	switch kind {
	case String:
		text, ok := value.(string)
		if !ok {
			c.invalid(value, kind, "only strings are accepted")
			return ""
		}
		return text
	case Int64Kind:
		return c.toInt64(value)
	case Float64Kind:
		return c.toFloat64(value)
	case BoolKind:
		return c.toBool(value)
	default:
		c.invalid(value, kind, "unsupported scalar target")
		return nil
	}
}

func (c *context) toInt64(value any) int64 {
	switch number := value.(type) {
	case int64:
		return number
	case float64:
		return c.floatToInt64(number)
	case string:
		parsed, err := strconv.ParseInt(number, 10, 64)
		if err == nil {
			return parsed
		}
		if isRangeError(err) {
			c.report(&Error{Category: Overflow, Target: Int64Kind, Original: number,
				Detail: fmt.Sprintf("integer text %q overflows int64", number)})
			return 0
		}
		c.invalid(number, Int64Kind, fmt.Sprintf("integer text %q is not a base-10 integer", number))
		return 0
	default:
		c.invalid(value, Int64Kind, "expected int64, float64, or integer text")
		return 0
	}
}

func (c *context) floatToInt64(number float64) int64 {
	if math.IsNaN(number) || math.IsInf(number, 0) {
		c.invalid(number, Int64Kind, "non-finite float cannot become int64")
		return 0
	}
	truncated := math.Trunc(number)
	if number < minInt64Float || number > maxInt64Float {
		bounded := int64(math.MaxInt64)
		if number < 0 {
			bounded = math.MinInt64
		}
		c.report(&Error{Category: Overflow, Target: Int64Kind, Original: number,
			Converted: bounded, Detail: fmt.Sprintf("float %v overflows int64", number)})
		return bounded
	}
	if math.Signbit(number) && number == 0 {
		c.report(&Error{Category: PrecisionLoss, Target: Int64Kind, Original: number,
			Converted: int64(0), Detail: "negative zero sign discarded; discarded fraction 0"})
		return 0
	}
	if fraction := number - truncated; fraction != 0 {
		c.report(&Error{Category: PrecisionLoss, Target: Int64Kind, Original: number,
			Converted: int64(truncated),
			Detail:    fmt.Sprintf("fractional part %.17g discarded", fraction)})
	}
	if magnitudeExceedsExactIntegerRange(number) {
		c.report(&Error{Category: PrecisionLoss, Target: Int64Kind, Original: number,
			Converted: int64(truncated), Detail: "magnitude exceeds exact float64 integer range 2^53"})
	}
	return int64(truncated)
}

func magnitudeExceedsExactIntegerRange(number float64) bool {
	absolute := math.Abs(number)
	if absolute < 9007199254740992.0 {
		return false
	}
	if absolute == 9007199254740992.0 {
		return false
	}
	return true
}

func (c *context) toFloat64(value any) float64 {
	switch number := value.(type) {
	case float64:
		return number
	case int64:
		converted := float64(number)
		if number < -9007199254740991 || number > 9007199254740991 {
			c.report(&Error{Category: PrecisionLoss, Target: Float64Kind, Original: number,
				Converted: converted,
				Detail:    fmt.Sprintf("integer %d is not exactly representable as float64", number)})
		}
		return converted
	case string:
		parsed, err := strconv.ParseFloat(number, 64)
		if err != nil {
			c.invalid(number, Float64Kind, fmt.Sprintf("float text %q is invalid", number))
			return 0
		}
		if math.IsInf(parsed, 0) {
			c.report(&Error{Category: Overflow, Target: Float64Kind, Original: number,
				Converted: parsed, Detail: fmt.Sprintf("float text %q overflows float64", number)})
		}
		return parsed
	default:
		c.invalid(value, Float64Kind, "expected int64, float64, or float text")
		return 0
	}
}

func (c *context) toBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case int64:
		if typed == 0 || typed == 1 {
			return typed == 1
		}
	case float64:
		if typed == 0 || typed == 1 {
			return typed == 1
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1":
			return true
		case "false", "0":
			return false
		}
	}
	c.invalid(value, BoolKind, "accepted values are exactly true, false, 1, 0, or their text forms")
	return false
}

func (c *context) invalid(value any, kind Kind, detail string) {
	c.report(&Error{Category: Invalid, Target: kind, Original: value, Detail: detail, skipped: true})
}
