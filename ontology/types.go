package ontology

import (
	"fmt"
	"math"
	"strconv"
)

// DataType 描述字段的取值类型。无损重解释能力由类型自身声明：
// CanReinterpret 回答“旧类型 old 下的合法取值 value，能否被无损地
// 重新解释为当前类型的一个合法取值”。判定是精确的，不允许近似比较。
type DataType interface {
	Name() string
	Validate(value Value) bool
	CanReinterpret(old DataType, value Value) bool
}

// Constraint 描述字段在其类型之上的取值约束。
// TighteningOf 判断 c 是否为比 other 更紧（取值集合严格缩小）的约束；
// LooseningOf 判断 c 是否为更松（取值集合严格扩大）的约束。
// 两者互斥；都不成立时由 Equivalent 判断是否同一约束，否则视为
// 不可比的复合变更（收紧与放宽同时存在）。
type Constraint interface {
	Satisfies(value Value) bool
	Equivalent(other Constraint) bool
	TighteningOf(other Constraint) bool
	LooseningOf(other Constraint) bool
}

// Value 是实例字段取值的统一表示。present==false 表示取值缺失，
// raw 恒为 Go 基本类型（int64/float64/string/bool），比较即精确比较。
type Value struct {
	present bool
	raw     any
}

func NewValue(raw any) Value {
	switch v := raw.(type) {
	case int:
		raw = int64(v)
	case int32:
		raw = int64(v)
	case float32:
		raw = float64(v)
	}
	return Value{present: true, raw: raw}
}

func Missing() Value { return Value{} }

func (v Value) Present() bool { return v.present }

func (v Value) Raw() any {
	if !v.present {
		return nil
	}
	return v.raw
}

func (v Value) String() string {
	if !v.present {
		return "<missing>"
	}
	return fmt.Sprintf("%v", v.raw)
}

// FieldDef 是某一版本下一个字段的完整定义。
type FieldDef struct {
	Name         string
	Type         DataType
	Constraint   Constraint
	AllowMissing bool
	HasDefault   bool
	Default      Value
}

// ---- 数据类型 ----------------------------------------------------------

type intType struct{}
type floatType struct{}
type stringType struct{}
type boolType struct{}

func NewIntType() DataType    { return intType{} }
func NewFloatType() DataType  { return floatType{} }
func NewStringType() DataType { return stringType{} }
func NewBoolType() DataType   { return boolType{} }

func (intType) Name() string    { return "int" }
func (floatType) Name() string  { return "float" }
func (stringType) Name() string { return "string" }
func (boolType) Name() string   { return "bool" }

func (intType) Validate(v Value) bool {
	_, ok := v.Raw().(int64)
	return v.Present() && ok
}

func (floatType) Validate(v Value) bool {
	_, ok := v.Raw().(float64)
	return v.Present() && ok
}

func (stringType) Validate(v Value) bool {
	_, ok := v.Raw().(string)
	return v.Present() && ok
}

func (boolType) Validate(v Value) bool {
	_, ok := v.Raw().(bool)
	return v.Present() && ok
}

// CanReinterpret 由“新类型”对“旧类型 + 旧取值”声明，全程精确：
//   - int -> int / int -> string(十进制精确表示) 无损；
//   - float -> float 无损；float -> int 仅当小数部分为零且不溢出时无损；
//   - bool -> bool / bool -> string("true"/"false") 无损；
//   - string -> int/float/bool 仅当字符串是该类型合法值的规范表示时无损
//     （禁止任何近似解析，例如 "1.0" 不接受为 int）；
//   - 其余跨类型组合均判不可逆。
func (intType) CanReinterpret(old DataType, v Value) bool {
	switch old.(type) {
	case intType:
		return true
	case floatType:
		f, ok := v.Raw().(float64)
		if !ok {
			return false
		}
		_, frac := math.Modf(f)
		return frac == 0
	case stringType:
		s, ok := v.Raw().(string)
		if !ok {
			return false
		}
		_, err := strconv.ParseInt(s, 10, 64)
		return err == nil
	}
	return false
}

func (floatType) CanReinterpret(old DataType, v Value) bool {
	switch old.(type) {
	case floatType:
		return true
	case intType:
		_, ok := v.Raw().(int64)
		return ok
	case stringType:
		s, ok := v.Raw().(string)
		if !ok {
			return false
		}
		// 只有解析成功且规范往返一致的字符串才算无损
		// （拒绝 "1e999" 溢出、"1.0" 非规范之类的近似解析）。
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return false
		}
		return strconv.FormatFloat(f, 'g', -1, 64) == s
	}
	return false
}

func (stringType) CanReinterpret(old DataType, v Value) bool {
	switch old.(type) {
	case stringType:
		return true
	case intType:
		_, ok := v.Raw().(int64)
		return ok
	case boolType:
		_, ok := v.Raw().(bool)
		return ok
	}
	return false
}

func (boolType) CanReinterpret(old DataType, v Value) bool {
	switch old.(type) {
	case boolType:
		return true
	case stringType:
		s, ok := v.Raw().(string)
		return ok && (s == "true" || s == "false")
	}
	return false
}

// ReinterpretValue 在 CanReinterpret 通过的前提下，精确地把旧取值
// 转换为新类型下的规范取值。返回 ok==false 表示无法无损重解释。
// 该函数与 CanReinterpret 一样不做任何近似比较。
func ReinterpretValue(newType, oldType DataType, v Value) (Value, bool) {
	if !newType.CanReinterpret(oldType, v) {
		return Missing(), false
	}
	switch newType.(type) {
	case intType:
		switch oldType.(type) {
		case intType:
			return v, true
		case floatType:
			f := v.Raw().(float64)
			return NewValue(int64(f)), true
		case stringType:
			n, err := strconv.ParseInt(v.Raw().(string), 10, 64)
			if err != nil {
				return Missing(), false
			}
			return NewValue(n), true
		}
	case floatType:
		switch oldType.(type) {
		case floatType:
			return v, true
		case intType:
			return NewValue(float64(v.Raw().(int64))), true
		case stringType:
			f, err := strconv.ParseFloat(v.Raw().(string), 64)
			if err != nil {
				return Missing(), false
			}
			return NewValue(f), true
		}
	case stringType:
		switch oldType.(type) {
		case stringType:
			return v, true
		case intType:
			return NewValue(strconv.FormatInt(v.Raw().(int64), 10)), true
		case boolType:
			return NewValue(strconv.FormatBool(v.Raw().(bool))), true
		}
	case boolType:
		switch oldType.(type) {
		case boolType:
			return v, true
		case stringType:
			b, err := strconv.ParseBool(v.Raw().(string))
			if err != nil {
				return Missing(), false
			}
			return NewValue(b), true
		}
	}
	return Missing(), false
}

// ---- 约束 --------------------------------------------------------------

// rangeConstraint：int64 闭区间。hasMin/hasMax 为 false 表示该侧无界。
type rangeConstraint struct {
	min, max       int64
	hasMin, hasMax bool
}

func NewRangeConstraint(min, max int64, hasMin, hasMax bool) Constraint {
	return rangeConstraint{min: min, max: max, hasMin: hasMin, hasMax: hasMax}
}

func (c rangeConstraint) Satisfies(v Value) bool {
	n, ok := v.Raw().(int64)
	if !ok {
		return false
	}
	if c.hasMin && n < c.min {
		return false
	}
	if c.hasMax && n > c.max {
		return false
	}
	return true
}

func (c rangeConstraint) Equivalent(o Constraint) bool {
	r, ok := o.(rangeConstraint)
	return ok && c == r
}

// 收紧：下界抬高或上界压低，且另一界不放松。
func (c rangeConstraint) TighteningOf(o Constraint) bool {
	r, ok := o.(rangeConstraint)
	if !ok || c == r {
		return false
	}
	lowerMovedUp := !r.hasMin || (c.hasMin && c.min > r.min)
	upperMovedDown := !r.hasMax || (c.hasMax && c.max < r.max)
	if !lowerMovedUp && !upperMovedDown {
		return false
	}
	// 新下界不得低于旧下界（不放松），新上界不得高于旧上界。
	if c.hasMin && r.hasMin && c.min < r.min {
		return false
	}
	if c.hasMax && r.hasMax && c.max > r.max {
		return false
	}
	return true
}

func (c rangeConstraint) LooseningOf(o Constraint) bool {
	r, ok := o.(rangeConstraint)
	return ok && r.TighteningOf(c)
}

// enumConstraint：字符串枚举。allowed 集合的严格子集即收紧。
type enumConstraint struct {
	allowed map[string]struct{}
}

func NewEnumConstraint(allowed []string) Constraint {
	m := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		m[a] = struct{}{}
	}
	return enumConstraint{allowed: m}
}

func (c enumConstraint) Satisfies(v Value) bool {
	s, ok := v.Raw().(string)
	if !ok {
		return false
	}
	_, ok = c.allowed[s]
	return ok
}

func (c enumConstraint) Equivalent(o Constraint) bool {
	e, ok := o.(enumConstraint)
	if !ok || len(c.allowed) != len(e.allowed) {
		return false
	}
	for k := range c.allowed {
		if _, ok := e.allowed[k]; !ok {
			return false
		}
	}
	return true
}

func (c enumConstraint) TighteningOf(o Constraint) bool {
	e, ok := o.(enumConstraint)
	if !ok || len(c.allowed) >= len(e.allowed) {
		return false
	}
	for k := range c.allowed {
		if _, ok := e.allowed[k]; !ok {
			return false
		}
	}
	return true
}

func (c enumConstraint) LooseningOf(o Constraint) bool {
	e, ok := o.(enumConstraint)
	return ok && e.TighteningOf(c)
}

// anyConstraint：接受任意类型合法取值，是所有约束中最松的。
type anyConstraint struct{}

func AnyValue() Constraint { return anyConstraint{} }

func (anyConstraint) Satisfies(Value) bool { return true }

func (anyConstraint) Equivalent(o Constraint) bool {
	_, ok := o.(anyConstraint)
	return ok
}

func (anyConstraint) TighteningOf(Constraint) bool { return false }

func (c anyConstraint) LooseningOf(o Constraint) bool {
	_, ok := o.(anyConstraint)
	return !ok
}
