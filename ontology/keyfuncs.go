package ontology

import "strconv"

// ExactKey 精确匹配键规则：显式值按其内容生成键，Absent 映射到保留键。
func ExactKey(v Value) Key {
	if !v.Present {
		return AbsentKey
	}
	return KeyOf(v.Data)
}

// PrefixKey 前缀键规则：按值的前 n 个字节分桶。
func PrefixKey(n int) KeyFunc {
	return func(v Value) Key {
		if !v.Present {
			return AbsentKey
		}
		s := v.Data
		if len(s) > n {
			s = s[:n]
		}
		return KeyOf("prefix:" + s)
	}
}

// LengthKey 长度桶键规则：按值的字符串长度分桶。
func LengthKey(v Value) Key {
	if !v.Present {
		return AbsentKey
	}
	return KeyOf("len:" + strconv.Itoa(len(v.Data)))
}
