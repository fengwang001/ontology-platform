package lsm

import (
	"fmt"
	"testing"

	"ontology/mem"
)

func buildTable(m int) *LSM {
	l := New()
	kvs := make([]mem.KV, 0, m)
	for i := 0; i < m; i++ {
		kvs = append(kvs, mem.KV{Key: fmt.Sprintf("k%06d", i),
			Entry: mem.Entry{Value: int64(i), Seq: int64(i + 1)}})
	}
	l.Freeze(kvs)
	return l
}

// TestProbeCountConstantInM 白盒断言：单个 SSTable 内定位 key 的检查数
// 必须是与 m 无关的小常数（map 定位），不得随 m 线性增长。
func TestProbeCountConstantInM(t *testing.T) {
	var first int = -1
	for _, m := range []int{100, 1000, 10000} {
		l := buildTable(m)
		l.Get("k000050") // 命中（所有档都存在该 key）
		if l.probe > probeBound {
			t.Fatalf("m=%d hit probe=%d > bound %d", m, l.probe, probeBound)
		}
		if first < 0 {
			first = l.probe
		} else if l.probe != first { // 各档完全相同，证明不随 m 增长
			t.Fatalf("probe drifted with m: %d vs %d", l.probe, first)
		}
		l.Get("k______missing") // 未命中
		if l.probe > probeBound {
			t.Fatalf("m=%d miss probe=%d > bound %d", m, l.probe, probeBound)
		}
	}
}

// TestCompactRules 表驱动：胜者墓碑在有更旧值时保留、唯一条目时丢弃。
func TestCompactRules(t *testing.T) {
	cases := []struct {
		name             string
		kvs              [][]mem.KV
		key              string
		wantHit, wantDel bool
	}{
		{"tombstone beats older value -> retained", [][]mem.KV{
			{{Key: "k", Entry: mem.Entry{Value: 9, Seq: 1}}},
			{{Key: "k", Entry: mem.Entry{Seq: 2, Deleted: true}}},
		}, "k", true, true},
		{"lone tombstone -> dropped", [][]mem.KV{
			{{Key: "k", Entry: mem.Entry{Seq: 5, Deleted: true}}},
		}, "k", false, false},
		{"newer value beats tombstone", [][]mem.KV{
			{{Key: "k", Entry: mem.Entry{Seq: 1, Deleted: true}}},
			{{Key: "k", Entry: mem.Entry{Value: 7, Seq: 2}}},
		}, "k", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := New()
			for _, k := range tc.kvs {
				l.Freeze(k)
			}
			l.Compact()
			if l.TableCount() != 1 {
				t.Fatalf("tables after compact = %d, want 1", l.TableCount())
			}
			e, hit := l.Get(tc.key)
			if hit != tc.wantHit || (hit && e.Deleted != tc.wantDel) {
				t.Fatalf("got hit=%v del=%v, want hit=%v del=%v",
					hit, e.Deleted, tc.wantHit, tc.wantDel)
			}
		})
	}
}
