package ontology

import (
	"fmt"
	"reflect"
	"sort"
)

// Value 是属性值的通用表示。不同合并规则对值的实际类型有各自要求：
//   - MaxInt / MinInt: int64
//   - SetUnion:        []string（视为集合，输出为排序后的并集）
//   - LWW:             LWWValue
type Value = any

// LWWValue 是 LWW（Last-Writer-Wins）合并规则使用的值。
// 比较只依赖写入内容本身（逻辑时钟、写入者标识、数据），
// 不依赖写入到达的物理时刻。
type LWWValue struct {
	Clock  uint64 // 写入方声明的逻辑时钟
	Writer string // 写入者标识，用于时钟相同时的确定性决胜
	Data   string // 实际数据
}

// Equal 判断两个值是否相等（用于等价写检测与变更检测）。
func Equal(a, b Value) bool {
	return reflect.DeepEqual(a, b)
}

// cloneValue 深拷贝值中的可变部分（目前仅 []string）。
func cloneValue(v Value) Value {
	if s, ok := v.([]string); ok {
		cp := make([]string, len(s))
		copy(cp, s)
		return cp
	}
	return v
}

// asInt64 将值断言为 int64。
func asInt64(v Value) (int64, error) {
	n, ok := v.(int64)
	if !ok {
		return 0, fmt.Errorf("期望 int64，实际为 %T", v)
	}
	return n, nil
}

// asStringSlice 将值断言为 []string。
func asStringSlice(v Value) ([]string, error) {
	s, ok := v.([]string)
	if !ok {
		return nil, fmt.Errorf("期望 []string，实际为 %T", v)
	}
	return s, nil
}

// asLWW 将值断言为 LWWValue。
func asLWW(v Value) (LWWValue, error) {
	l, ok := v.(LWWValue)
	if !ok {
		return LWWValue{}, fmt.Errorf("期望 LWWValue，实际为 %T", v)
	}
	return l, nil
}

// unionSorted 计算两个字符串集合的并集，返回排序去重后的新切片。
// 输出只依赖输入内容，顺序确定。
func unionSorted(a, b []string) []string {
	set := make(map[string]struct{}, len(a)+len(b))
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		set[s] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
