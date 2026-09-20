package join

import (
	"cmp"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// family 连接键值的类型族。int64 与 float64 同属 famNum，按数值比较。
type family int

const (
	famBool family = iota
	famNum
	famStr
)

// keyVal 是一个已规范化的连接键列值。
type keyVal struct {
	fam family
	b   bool
	rat *big.Rat // famNum 且非 Inf 时非 nil，保证 int64/float64 精确比较
	inf int      // famNum 专用：-1 表示 -Inf，+1 表示 +Inf，0 表示有限值
	s   string
}

// keyValOf 把任意属性值规范化为 keyVal。
// null=true 表示该值按 SQL 三值语义视为"空"（nil 或 NaN），永不匹配；
// ok=false 表示类型不受支持。
func keyValOf(v any) (kv keyVal, null bool, ok bool) {
	switch t := v.(type) {
	case nil:
		return keyVal{}, true, true
	case bool:
		return keyVal{fam: famBool, b: t}, false, true
	case string:
		return keyVal{fam: famStr, s: t}, false, true
	case int64:
		return keyVal{fam: famNum, rat: big.NewRat(t, 1)}, false, true
	case float64:
		switch {
		case math.IsNaN(t):
			return keyVal{}, true, true
		case math.IsInf(t, 1):
			return keyVal{fam: famNum, inf: 1}, false, true
		case math.IsInf(t, -1):
			return keyVal{fam: famNum, inf: -1}, false, true
		}
		rat := new(big.Rat)
		rat.SetFloat64(t) // 精确表示；-0.0 与 +0.0 都规范化为 0
		return keyVal{fam: famNum, rat: rat}, false, true
	}
	return keyVal{}, false, false
}

// compareKeyVal 给出键值的全序：先按类型族（bool < 数值 < 字符串），
// 族内 bool 按 false<true，数值按大小（-Inf 最小、+Inf 最大），字符串按字典序。
func compareKeyVal(a, b keyVal) int {
	if a.fam != b.fam {
		return cmp.Compare(a.fam, b.fam)
	}
	switch a.fam {
	case famBool:
		switch {
		case a.b == b.b:
			return 0
		case !a.b:
			return -1
		default:
			return 1
		}
	case famNum:
		if a.inf != b.inf {
			return cmp.Compare(a.inf, b.inf)
		}
		if a.inf != 0 {
			return 0
		}
		return a.rat.Cmp(b.rat)
	default:
		return strings.Compare(a.s, b.s)
	}
}

// encode 把键值编码为可放入 map 键的字符串，带类型族标签。
func (k keyVal) encode() string {
	switch k.fam {
	case famBool:
		if k.b {
			return "b:1"
		}
		return "b:0"
	case famNum:
		switch k.inf {
		case -1:
			return "n:-Inf"
		case 1:
			return "n:+Inf"
		}
		return "n:" + k.rat.RatString()
	default:
		return "s:" + k.s
	}
}

// encodeKey 把整组键值编码为单个字符串，逐列长度前缀避免歧义。
func encodeKey(vals []keyVal) string {
	var b strings.Builder
	for _, v := range vals {
		s := v.encode()
		b.WriteString(strconv.Itoa(len(s)))
		b.WriteByte(':')
		b.WriteString(s)
	}
	return b.String()
}

// extractKey 提取一行的连接键。nullKey=true 表示键为空
// （任一列属性缺失、值为 nil 或为 NaN），该行永不匹配。
func extractKey(r Row, keys []string, side string) (vals []keyVal, nullKey bool, err error) {
	vals = make([]keyVal, len(keys))
	for i, k := range keys {
		v, exists := r[k]
		if !exists {
			return nil, true, nil
		}
		kv, null, ok := keyValOf(v)
		if !ok {
			return nil, false, &UnsupportedTypeError{
				Key: k, Side: side, Type: fmt.Sprintf("%T", v),
			}
		}
		if null {
			return nil, true, nil
		}
		vals[i] = kv
	}
	return vals, false, nil
}

// validateKeyTypes 在连接前校验每个键列的类型族一致性，
// 保证比较严格且可判定：冲突时返回 *TypeError 而不是判为不等。
func validateKeyTypes(left, right []Row, keys []string) error {
	for _, k := range keys {
		lf, lt, err := columnFamily(left, k, "left")
		if err != nil {
			return err
		}
		rf, rt, err := columnFamily(right, k, "right")
		if err != nil {
			return err
		}
		if lf >= 0 && rf >= 0 && lf != rf {
			return &TypeError{Key: k, Side: "cross", TypeA: lt, TypeB: rt}
		}
	}
	return nil
}

// columnFamily 返回某张表某键列上非空值的类型族（-1 表示该列全为空）。
// 单侧内部混用不同类型族时返回 *TypeError。
func columnFamily(rows []Row, key, side string) (family, string, error) {
	fam := family(-1)
	typ := ""
	for _, r := range rows {
		v, exists := r[key]
		if !exists {
			continue
		}
		kv, null, ok := keyValOf(v)
		if !ok {
			return 0, "", &UnsupportedTypeError{
				Key: key, Side: side, Type: fmt.Sprintf("%T", v),
			}
		}
		if null {
			continue
		}
		if fam < 0 {
			fam = kv.fam
			typ = fmt.Sprintf("%T", v)
			continue
		}
		if kv.fam != fam {
			return 0, "", &TypeError{
				Key: key, Side: side,
				TypeA: typ, TypeB: fmt.Sprintf("%T", v),
			}
		}
	}
	return fam, typ, nil
}
