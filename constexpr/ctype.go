package constexpr

import "math/big"

// CType 是有类型常量的具体类型。
type CType uint8

const (
	CTypeInvalid CType = iota
	CTypeI8
	CTypeI16
	CTypeI32
	CTypeI64
	CTypeU8
	CTypeU16
	CTypeU32
	CTypeU64
	CTypeF64
	CTypeBool
	CTypeString
)

// ParseCType 按规范名称解析类型，未知名称返回 false（参数非法）。
func ParseCType(name string) (CType, bool) {
	t, ok := cTypeByName[name]
	return t, ok
}

func (t CType) String() string {
	if int(t) < len(cTypeNames) {
		return cTypeNames[t]
	}
	return "<invalid-type>"
}

// Kind 返回该类型承载值的种类。
func (t CType) Kind() Kind { return cTypeKinds[t] }

// IsInt 返回是否为有符号或无符号整型类型。
func (t CType) IsInt() bool { return t >= CTypeI8 && t <= CTypeU64 }

// Signed 返回整型类型是否有符号；非整型返回 false。
func (t CType) Signed() bool { return t >= CTypeI8 && t <= CTypeI64 }

// BitWidth 返回整型类型的位宽；非整型返回 0。
func (t CType) BitWidth() int { return cTypeWidths[t] }

// intRange 返回整型类型的闭区间 [min,max]。
func (t CType) intRange() (min, max *big.Int) {
	switch t {
	case CTypeI8:
		return big.NewInt(-1 << 7), big.NewInt(1<<7 - 1)
	case CTypeI16:
		return big.NewInt(-1 << 15), big.NewInt(1<<15 - 1)
	case CTypeI32:
		return big.NewInt(-1 << 31), big.NewInt(1<<31 - 1)
	case CTypeI64:
		return new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 63)),
			new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 63), big.NewInt(1))
	case CTypeU8:
		return big.NewInt(0), big.NewInt(1<<8 - 1)
	case CTypeU16:
		return big.NewInt(0), big.NewInt(1<<16 - 1)
	case CTypeU32:
		return big.NewInt(0), big.NewInt(1<<32 - 1)
	case CTypeU64:
		return big.NewInt(0), new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(1))
	}
	return nil, nil
}

var cTypeByName = map[string]CType{
	"i8": CTypeI8, "i16": CTypeI16, "i32": CTypeI32, "i64": CTypeI64,
	"u8": CTypeU8, "u16": CTypeU16, "u32": CTypeU32, "u64": CTypeU64,
	"f64": CTypeF64, "bool": CTypeBool, "string": CTypeString,
}

var cTypeNames = [...]string{
	CTypeInvalid: "<invalid-type>",
	CTypeI8:      "i8", CTypeI16: "i16", CTypeI32: "i32", CTypeI64: "i64",
	CTypeU8: "u8", CTypeU16: "u16", CTypeU32: "u32", CTypeU64: "u64",
	CTypeF64: "f64", CTypeBool: "bool", CTypeString: "string",
}

var cTypeKinds = [...]Kind{
	CTypeInvalid: KindInt,
	CTypeI8:      KindInt, CTypeI16: KindInt, CTypeI32: KindInt, CTypeI64: KindInt,
	CTypeU8: KindInt, CTypeU16: KindInt, CTypeU32: KindInt, CTypeU64: KindInt,
	CTypeF64: KindRat, CTypeBool: KindBool, CTypeString: KindString,
}

var cTypeWidths = [...]int{
	CTypeI8: 8, CTypeI16: 16, CTypeI32: 32, CTypeI64: 64,
	CTypeU8: 8, CTypeU16: 16, CTypeU32: 32, CTypeU64: 64,
}
