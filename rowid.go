package join

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"sort"
)

// RowID 是行内容的确定性标识：encodeRow 输出的 SHA-256 十六进制串。
// 内容相同的行标识相同，与行在输入切片中的位置及 map 迭代顺序无关。
type RowID string

const (
	tagNil = iota
	tagBool
	tagInt
	tagFloat
	tagString
	tagBytes
	tagOther
)

// encodeRow 的字节布局（连续拼接，无歧义）：
//
//	uint32 键数
//	对每个按 UTF-8 升序排列的键：
//	  uint32 len(key) | key 字节 | 1 字节类型标签 | uint32 len(v) | v 字节
//
// 值的规范化字节：
//
//   - nil：空；bool：0/1；整数：大端 int64；
//   - float64：大端 Float64bits，NaN 统一规范化为 qNaN 位模式；
//   - string：UTF-8；[]byte：原字节；
//   - 其它：reflect 类型名 + '=' + fmt.Sprintf("%#v")。
func encodeRow(row map[string]any) []byte {
	keys := make([]string, 0, len(row))
	for k := range row {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, uint32(len(keys)))

	for _, k := range keys {
		buf = appendString(buf, k)
		tag, val := canonicalValue(row[k])
		buf = append(buf, tag)
		buf = appendBytes(buf, val)
	}
	return buf
}

func rowIDOf(row map[string]any) RowID {
	sum := sha256.Sum256(encodeRow(row))
	return RowID(hex.EncodeToString(sum[:]))
}

func canonicalValue(v any) (byte, []byte) {
	switch x := v.(type) {
	case nil:
		return tagNil, nil
	case bool:
		if x {
			return tagBool, []byte{1}
		}
		return tagBool, []byte{0}
	case int:
		return tagInt, int64Bytes(int64(x))
	case int8:
		return tagInt, int64Bytes(int64(x))
	case int16:
		return tagInt, int64Bytes(int64(x))
	case int32:
		return tagInt, int64Bytes(int64(x))
	case int64:
		return tagInt, int64Bytes(x)
	case uint:
		return tagInt, int64Bytes(int64(x))
	case uint8:
		return tagInt, int64Bytes(int64(x))
	case uint16:
		return tagInt, int64Bytes(int64(x))
	case uint32:
		return tagInt, int64Bytes(int64(x))
	case uint64:
		return tagInt, int64Bytes(int64(x))
	case float32:
		return tagFloat, float64Bytes(float64(x))
	case float64:
		return tagFloat, float64Bytes(x)
	case string:
		return tagString, []byte(x)
	case []byte:
		return tagBytes, x
	default:
		s := reflect.TypeOf(v).String() + "=" + fmt.Sprintf("%#v", v)
		return tagOther, []byte(s)
	}
}

func int64Bytes(v int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(v))
	return b
}

func float64Bytes(v float64) []byte {
	if math.IsNaN(v) {
		v = math.Float64frombits(0x7FF8000000000000)
	}
	return int64Bytes(int64(math.Float64bits(v)))
}

func appendString(buf []byte, s string) []byte {
	l := make([]byte, 4)
	binary.BigEndian.PutUint32(l, uint32(len(s)))
	return append(append(buf, l...), s...)
}

func appendBytes(buf, b []byte) []byte {
	l := make([]byte, 4)
	binary.BigEndian.PutUint32(l, uint32(len(b)))
	return append(append(buf, l...), b...)
}

// deepCopyRow 返回与输入完全脱钩的行副本；递归复制嵌套 map/slice，
// 标量直接按值携带。
func deepCopyRow(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return deepCopyRow(x)
	case []any:
		cp := make([]any, len(x))
		for i, e := range x {
			cp[i] = deepCopyValue(e)
		}
		return cp
	default:
		return v
	}
}
