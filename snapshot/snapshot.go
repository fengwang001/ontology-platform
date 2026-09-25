// Package snapshot 提供对象状态的冻结快照：不可变、按属性名排序、字节级确定。
package snapshot

import (
	"bytes"
	"encoding/json"
	"sort"
)

type state struct {
	objType string
	keys    []string
	vals    map[string]any
	frozen  []byte
	reads   int
}

// Snapshot 是某一时刻对象状态的不可变视图。字段不导出、无写方法，
// 钩子只能读取，编译期即无法通过 Snapshot 改动对象状态。
type Snapshot struct {
	st *state
}

// Freeze 冻结 objType 类型对象在 attrs 状态下的快照。
// 对入参做拷贝：调用方后续改动 attrs 不会影响已冻结快照。
func Freeze(objType string, attrs map[string]any) Snapshot {
	vals := make(map[string]any, len(attrs))
	for k, v := range attrs {
		vals[k] = copyValue(v)
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return Snapshot{st: &state{
		objType: objType,
		keys:    keys,
		vals:    vals,
		frozen:  encode(objType, keys, vals),
	}}
}

// Type 返回对象类型名。
func (s Snapshot) Type() string { return s.st.objType }

// Get 按属性名读取值。
func (s Snapshot) Get(name string) (any, bool) {
	s.st.reads++
	v, ok := s.st.vals[name]
	return v, ok
}

// Keys 返回排序后的属性名。
func (s Snapshot) Keys() []string {
	s.st.reads++
	out := make([]string, len(s.st.keys))
	copy(out, s.st.keys)
	return out
}

// Bytes 返回快照的确定性字节编码（属性名排序后逐对编码）。
func (s Snapshot) Bytes() []byte {
	out := make([]byte, len(s.st.frozen))
	copy(out, s.st.frozen)
	return out
}

// Reads 返回该快照被读取的次数（非导出计数器 reads 的只读视图）。
func (s Snapshot) Reads() int { return s.st.reads }

// encode 按属性名排序逐对编码；值用 JSON 编码（Go 对 map 键排序），
// 同一逻辑状态无论入参 map 迭代顺序如何，输出逐字节相同。
func encode(objType string, keys []string, vals map[string]any) []byte {
	var buf bytes.Buffer
	buf.WriteString("type:")
	buf.WriteString(objType)
	buf.WriteByte('\n')
	for _, k := range keys {
		kb, _ := json.Marshal(k)
		vb, err := json.Marshal(vals[k])
		if err != nil {
			vb, _ = json.Marshal(err.Error())
		}
		buf.Write(kb)
		buf.WriteByte('=')
		buf.Write(vb)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// copyValue 对 map/slice 做防御性拷贝，使快照与调用方彻底解耦。
func copyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, inner := range t {
			out[k] = copyValue(inner)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, inner := range t {
			out[i] = copyValue(inner)
		}
		return out
	default:
		return v
	}
}
