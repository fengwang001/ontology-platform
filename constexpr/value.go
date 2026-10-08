package constexpr

import "math/big"

// Value 是一个已求定的值：种类 + 可选具体类型 + 有效载荷。
// Value 在求值器内部传递时不可变；从外部接收与向外部返回时均做拷贝。
type Value struct {
	kind  Kind
	ctype CType // CTypeInvalid 表示无类型常量
	i     *big.Int
	r     *big.Rat
	b     bool
	s     string
}

// Kind 返回值的种类。
func (v *Value) Kind() Kind { return v.kind }

// IsTyped 返回是否为有类型常量。
func (v *Value) IsTyped() bool { return v.ctype != CTypeInvalid }

// Type 返回具体类型；无类型时返回 CTypeInvalid。
func (v *Value) Type() CType { return v.ctype }

// Int 返回整数值的拷贝（仅 KindInt）。
func (v *Value) Int() *big.Int {
	if v.kind != KindInt || v.i == nil {
		panic("constexpr: Int() on non-int value")
	}
	return new(big.Int).Set(v.i)
}

// Rat 返回有理数值的拷贝（KindRat；KindInt 时也返回其作为有理数的值）。
func (v *Value) Rat() *big.Rat {
	switch v.kind {
	case KindRat:
		if v.r == nil {
			panic("constexpr: Rat() on invalid rat value")
		}
		return new(big.Rat).Set(v.r)
	case KindInt:
		return new(big.Rat).SetInt(v.i)
	}
	panic("constexpr: Rat() on non-numeric value")
}

// Bool 返回布尔值（仅 KindBool）。
func (v *Value) Bool() bool { return v.b }

// String 返回字符串值（仅 KindString）。
func (v *Value) String() string {
	if v.kind != KindString {
		panic("constexpr: String() on non-string value")
	}
	return v.s
}

// Clone 返回深拷贝。
func (v *Value) Clone() *Value {
	c := &Value{kind: v.kind, ctype: v.ctype, b: v.b, s: v.s}
	if v.i != nil {
		c.i = new(big.Int).Set(v.i)
	}
	if v.r != nil {
		c.r = new(big.Rat).Set(v.r)
	}
	return c
}

// asRat 返回表示该数值的精确有理数（新对象，可安全修改）。
func (v *Value) asRat() *big.Rat {
	if v.kind == KindInt {
		return new(big.Rat).SetInt(v.i)
	}
	return new(big.Rat).Set(v.r)
}

// asInt 要求值本身是整数种类，返回内部整数（不可变使用）。
func (v *Value) asInt() *big.Int { return v.i }

// 内部构造器：产生不可变约定的 Value。

func untypedInt(i *big.Int) *Value {
	return &Value{kind: KindInt, ctype: CTypeInvalid, i: i}
}

func untypedRat(r *big.Rat) *Value {
	return &Value{kind: KindRat, ctype: CTypeInvalid, r: r}
}

func untypedBool(b bool) *Value {
	return &Value{kind: KindBool, ctype: CTypeInvalid, b: b}
}

func untypedString(s string) *Value {
	return &Value{kind: KindString, ctype: CTypeInvalid, s: s}
}

// typedInt 复制 i 并标记类型；调用前须保证 i 可表示于 t。
func typedInt(i *big.Int, t CType) *Value {
	return &Value{kind: KindInt, ctype: t, i: new(big.Int).Set(i)}
}

// typedRat 以精确有理数 r 作为 f64 常量的载荷；舍入后的可表示性已检查。
func typedRat(r *big.Rat) *Value {
	return &Value{kind: KindRat, ctype: CTypeF64, r: new(big.Rat).Set(r)}
}

func typedBool(b bool) *Value {
	return &Value{kind: KindBool, ctype: CTypeBool, b: b}
}

func typedString(s string) *Value {
	return &Value{kind: KindString, ctype: CTypeString, s: s}
}

// Float64 对 f64 常量返回其当前的双精度舍入值；其他类型 panic。
func (v *Value) Float64() float64 {
	if v.ctype != CTypeF64 {
		panic("constexpr: Float64() on non-f64 value")
	}
	return ratToFloat64(v.r)
}
