package ontology

import (
	"fmt"
	"testing"
)

// 这些内部测试直接构造 snapshot，用于验证判定结果不依赖遍历顺序：
// 即便打乱对象枚举顺序与邻接表顺序，findCanonicalCycle 也必须返回
// 完全相同的规范环。

// findCycleInOrder 用给定的对象顺序与邻接表顺序执行同样的判定，
// 返回规范环序列（内部测试入口）。
func findCycleInOrder(objs []string, out map[string][]string) []string {
	s := &snapshot{
		objects: objs,
		visible: map[string]struct{}{},
		out:     out,
		count:   len(objs),
	}
	for _, id := range objs {
		s.visible[id] = struct{}{}
	}
	return findCanonicalCycle(s)
}

func TestOrderIndependenceInternal(t *testing.T) {
	objs := []string{"a", "b", "c", "d", "e"}
	out := map[string][]string{
		"a": {"b", "c"},
		"b": {"d"},
		"c": {"d"},
		"d": {"a", "e"},
		"e": {"a"},
	}
	outRev := map[string][]string{}
	for k, v := range out {
		r := make([]string, len(v))
		for i := range v {
			r[i] = v[len(v)-1-i]
		}
		outRev[k] = r
	}
	canonical := findCycleInOrder(objs, out)
	shuffled := findCycleInOrder([]string{"d", "a", "e", "c", "b"}, outRev)
	if fmt.Sprint(canonical) != fmt.Sprint(shuffled) {
		t.Fatalf("result depends on start/order: %v vs %v", canonical, shuffled)
	}
	if len(canonical) != 3 {
		t.Fatalf("want shortest cycle length 3, got %v", canonical)
	}
}
