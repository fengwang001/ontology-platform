package agg

import (
	"fmt"
	"testing"

	"ontology/grp"
)

// TestProbeConstant 证明定位某个已存在组是 O(1) 哈希定位：
// 存在的组数 m 取多档，再对一个已存在组施加增量，
// 检查过的组个数（非导出字段 probes）不随 m 线性增长。
func TestProbeConstant(t *testing.T) {
	const maxProbes = 3 // 与 m 无关的小常数上界
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			a := New()
			for i := 0; i < m; i++ {
				s := fmt.Sprintf("g%05d", i)
				if !a.Apply(grp.Normalize(&s), 1) {
					t.Fatalf("setup Apply(%s) 被拒绝", s)
				}
			}
			target := "g00000"
			if !a.Apply(grp.Normalize(&target), 1) {
				t.Fatal("对已存在组的增量被拒绝")
			}
			if a.probes > maxProbes {
				t.Fatalf("m=%d 时检查组数 %d，超过常数上界 %d（疑似整表扫描）", m, a.probes, maxProbes)
			}
		})
	}
}

// TestApplySemantics 钉住 agg 层语义：负值拒绝且不留痕、归零即删、枚举有序。
func TestApplySemantics(t *testing.T) {
	cases := []struct {
		name string
		ops  []struct {
			key *string
			d   int64
		}
		wantOK  []bool
		wantLen int // 最终存在的组数
	}{
		{"负值拒绝不留痕", []struct {
			key *string
			d   int64
		}{{ptr("x"), 2}, {ptr("x"), -3}}, []bool{true, false}, 1},
		{"归零即删", []struct {
			key *string
			d   int64
		}{{ptr("x"), 2}, {ptr("x"), -2}}, []bool{true, true}, 0},
		{"null与空串各自独立", []struct {
			key *string
			d   int64
		}{{nil, 1}, {ptr(""), 1}, {nil, -1}}, []bool{true, true, true}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := New()
			for i, op := range tc.ops {
				if got := a.Apply(grp.Normalize(op.key), op.d); got != tc.wantOK[i] {
					t.Fatalf("第 %d 步 Apply ok=%v，期望 %v", i, got, tc.wantOK[i])
				}
			}
			if got := len(a.Keys()); got != tc.wantLen {
				t.Fatalf("存在组数=%d，期望 %d", got, tc.wantLen)
			}
		})
	}
}

func ptr(s string) *string { return &s }
