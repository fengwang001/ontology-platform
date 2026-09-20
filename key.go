
package join

import (
	"errors"
	"math"
	"math/big"
)

// 连接键类型族：bool、数值（所有整数与浮点）、string 各自独立；
// 跨族遇到同名键不判不等，而是报类型冲突。
const (
	familyBool   = 'b'
	familyNumber = 'n'
	familyString = 's'
)

// ErrUnsupportedKeyType 表示连接键出现了不支持作为键的 Go 类型。
var ErrUnsupportedKeyType = errors.New("join: unsupported join-key type")

// keyValue 是单个连接键属性抽取后的可比较形态。
type keyValue struct {
	null    bool
	family  byte
	boolean bool
	number  *big.Rat
	text    string
}

// extractKey 从一行中抽取某个连接键。
//
// 空（null）：属性缺失、值为 nil、float NaN（含 float32 NaN）。
// 空键不携带类型族，因此两个空键彼此也不相等。
func extractKey(row map[string]any, name string) (keyValue, error) {
	v, ok := row[name]
	if !ok || v == nil {
		return keyValue{null: true}, nil
	}
	switch x := v.(type) {
	case bool:
		return keyValue{family: familyBool, boolean: x}, nil
	case string:
		return keyValue{family: familyString, text: x}, nil
	case int:
		return numberKey(int64(x)), nil
	case int8:
		return numberKey(int64(x)), nil
	case int16:
		return numberKey(int64(x)), nil
	case int32:
		return numberKey(int64(x)), nil
	case int64:
		return numberKey(x), nil
	case uint:
		return numberKeyUint(uint64(x)), nil
	case uint8:
		return numberKeyUint(uint64(x)), nil
	case uint16:
		return numberKeyUint(uint64(x)), nil
	case uint32:
		return numberKeyUint(uint64(x)), nil
	case uint64:
		return numberKeyUint(x), nil
	case float32:
		f := float64(x)
		if math.IsNaN(f) {
			return keyValue{null: true}, nil
		}
		return keyValue{family: familyNumber, number: new(big.Rat).SetFloat64(f)}, nil
	case float64:
		if math.IsNaN(x) {
			return keyValue{null: true}, nil
		}
		return keyValue{family: familyNumber, number: new(big.Rat).SetFloat64(x)}, nil
	default:
		return keyValue{}, ErrUnsupportedKeyType
	}
}

func numberKey(v int64) keyValue {
	return keyValue{family: familyNumber, number: big.NewRat(v, 1)}
}

func numberKeyUint(v uint64) keyValue {
	r := new(big.Rat).SetUint64(v)
	return keyValue{family: familyNumber, number: r}
}

// token 返回“抹去类型族差异后”的值编码，仅用于索引桶的定位；
// 同 token 不同 family 的命中由索引层转成类型冲突错误。
func (k keyValue) token() string {
	switch k.family {
	case familyBool:
		if k.boolean {
			return "b:1"
		}
		return "b:0"
	case familyString:
		return "s:" + k.text
	default:
		return "n:" + k.number.RatString()
	}
}

// compareKey 对两列做严格升序比较：null 最先，其后 bool < number < string。
// 返回 -1/0/1。
func compareKey(a, b keyValue) int {
	switch {
	case a.null && b.null:
		return 0
	case a.null:
		return -1
	case b.null:
		return 1
	}
	if a.family != b.family {
		if a.family < b.family {
			return -1
		}
		return 1
	}
	switch a.family {
	case familyBool:
		switch {
		case a.boolean == b.boolean:
			return 0
		case !a.boolean:
			return -1
		default:
			return 1
		}
	case familyNumber:
		return a.number.Cmp(b.number)
	default:
		switch {
		case a.text < b.text:
			return -1
		case a.text > b.text:
			return 1
		default:
			return 0
		}
	}
}

