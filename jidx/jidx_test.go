package jidx

import (
	"fmt"
	"testing"

	"ontology/rel"
)

// TestDelSFanoutScans 钉住删除复杂度：N 条 R 中只有 m 条 R[a]=b，
// DelC 经反向索引 byB[b] 扇出，检查过的 R 条目数必须恒为 m，不随 N 线性增长。
func TestDelSFanoutScans(t *testing.T) {
	cases := []int{100, 1000, 10000}
	const m = 3
	prev := -1
	for _, N := range cases {
		t.Run(fmt.Sprintf("N=%d", N), func(t *testing.T) {
			st := rel.New()
			ix := New(st)
			for i := 0; i < N; i++ {
				b := 11 + i%97 // 其他 a 永不映射到 10
				if i < m {
					b = 10
				}
				st.SetR(i, b)
				ix.BindR(i, b)
			}
			if err := st.AddS(10, 100); err != nil {
				t.Fatal(err)
			}
			ix.AddC(10, 100)
			if ix.Size() != m {
				t.Fatalf("Size=%d want %d before DelC", ix.Size(), m)
			}
			ix.DelC(10, 100)
			if ix.delChecks != m {
				t.Fatalf("DelC examined %d R entries, want %d (must not scale with N=%d)",
					ix.delChecks, m, N)
			}
			if ix.Size() != 0 {
				t.Fatalf("Size=%d want 0 after fan-out delete", ix.Size())
			}
			if prev >= 0 && ix.delChecks != prev {
				t.Fatalf("examined count grew with N: %d then %d", prev, ix.delChecks)
			}
			prev = ix.delChecks
		})
	}
}

// TestDelRChecksZero 钉住 DelR 路径：UnbindR 经 byA[a] 直接定位，
// 检查的 R 条目数为 0，同样不扫全表。
func TestDelRChecksZero(t *testing.T) {
	st := rel.New()
	ix := New(st)
	const N = 500
	for i := 0; i < N; i++ {
		b := 1000 + i // 每个 a 独占一个 b
		st.SetR(i, b)
		ix.BindR(i, b)
	}
	if err := st.AddS(10, 77); err != nil {
		t.Fatal(err)
	}
	ix.AddC(10, 77) // 此时 byB[10] 为空，扇出 0 个 a
	st.SetR(0, 10)
	ix.RebindR(0, 1000, 10) // BindR 枚举 S[10]={77} 建唯一一对 (0,77)
	if ix.Size() != 1 {
		t.Fatalf("Size=%d want 1 before UnbindR", ix.Size())
	}
	ix.UnbindR(0, 10)
	if ix.delChecks != 0 {
		t.Fatalf("UnbindR examined %d R entries, want 0", ix.delChecks)
	}
	if got := ix.Join(0); len(got) != 0 {
		t.Fatalf("Join(0)=%v after UnbindR, want empty", got)
	}
	if _, present := ix.byA[0]; present {
		t.Fatal("byA[0] left behind")
	}
	if _, present := ix.byB[10]; present {
		t.Fatalf("byB[10] not cleaned")
	}
	if ix.Size() != 0 {
		t.Fatalf("Size=%d want 0 after UnbindR", ix.Size())
	}
}
