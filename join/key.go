package join

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// kind 是连接键值的规范类别。数值的 int 与 float 细分两种存储，
// 但在排序与相等判断中同属数值一族。
type kind byte

const (
	kindInt kind = iota
	kindFloat
	kindString
	kindBool
)

// canonVal 是一个连接键列的规范化值，可直接作为 map 键使用。
type canonVal struct {
	kind kind
	i    int64
	f    float64
	s    string
	b    bool
}

// empty 表示该键为空（属性缺失、nil 或 NaN），不参与相等比较。
type keyVal struct {
	empty bool
	val   canonVal
}

// canonKey 是一行全部连接键列的规范化组合，可直接作为 map 键。
type canonKey struct {
	parts string
}

// keyOf 把一行在 keys 上的值规范化。任一列为空时返回 null=true。
func keyOf(row Row, keys []string) (canonKey, []keyVal, bool) {
	vals := make([]keyVal, len(keys))
	var sb strings.Builder
	null := false
	for i, k := range keys {
		v, ok := row[k]
		kv := normalize(v, ok)
		vals[i] = kv
		if kv.empty {
			null = true
		}
		sb.WriteString(kv.val.encode())
		sb.WriteByte(0)
	}
	return canonKey{parts: sb.String()}, vals, null
}

// normalize 把任意值转成 keyVal；ok=false（属性缺失）、nil、NaN 都视为空。
// 不支持的类型原样保留，由 validateKeyTypes 报错。
func normalize(v any, ok bool) keyVal {
	if !ok || v == nil {
		return keyVal{empty: true}
	}
	switch t := v.(type) {
	case string:
		return keyVal{val: canonVal{kind: kindString, s: t}}
	case bool:
		return keyVal{val: canonVal{kind: kindBool, b: t}}
	case int:
		return keyVal{val: canonVal{kind: kindInt, i: int64(t)}}
	case int64:
		return keyVal{val: canonVal{kind: kindInt, i: t}}
	case float64:
		if math.IsNaN(t) {
			return keyVal{empty: true}
		}
		return keyVal{val: canonFloat(t)}
	default:
		return keyVal{val: canonVal{kind: kindString, s: unsupportedMarker(v)}}
	}
}

// unsupportedPrefix 让不支持的类型在编码后与正常 string 区分开。
const unsupportedPrefix = "\x01unsupported:"

func unsupportedMarker(v any) string {
	return unsupportedPrefix + fmt.Sprintf("%T", v)
}

// isUnsupported 报告该规范值是否来自不支持的类型。
func (c canonVal) isUnsupported() bool {
	return c.kind == kindString && strings.HasPrefix(c.s, unsupportedPrefix)
}

// canonFloat 规范化 float64：±0 统一为 +0；能精确表示为 int64 的
// 整数值转成 int，使 int64 与 float64 按数值相等落到同一组。
func canonFloat(f float64) canonVal {
	if f == 0 {
		f = 0 // 把 -0.0 归一为 +0.0
	}
	const maxExactInt64 = 9223372036854775808.0 // 2^63，float64 可精确表示
	if f == math.Trunc(f) && f >= -maxExactInt64 && f < maxExactInt64 {
		return canonVal{kind: kindInt, i: int64(f)}
	}
	return canonVal{kind: kindFloat, f: f}
}

// encode 输出规范值的确定性字符串编码（含类型标签）。
func (c canonVal) encode() string {
	switch c.kind {
	case kindInt:
		return "i:" + strconv.FormatInt(c.i, 10)
	case kindFloat:
		return "f:" + strconv.FormatUint(math.Float64bits(c.f), 16)
	case kindBool:
		if c.b {
			return "b:1"
		}
		return "b:0"
	default:
		return "s:" + strconv.Itoa(len(c.s)) + ":" + c.s
	}
}

// typeName 给出规范值面向用户的类型名。
func (c canonVal) typeName() string {
	switch c.kind {
	case kindInt:
		return "int64"
	case kindFloat:
		return "float64"
	case kindBool:
		return "bool"
	default:
		if c.isUnsupported() {
			return strings.TrimPrefix(c.s, unsupportedPrefix)
		}
		return "string"
	}
}

// numeric 报告该值是否属于数值一族（int64 与 float64 互可比）。
func (c canonVal) numeric() bool {
	return c.kind == kindInt || c.kind == kindFloat
}
