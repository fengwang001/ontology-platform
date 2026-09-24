package inst

import (
	"fmt"
	"testing"

	"ontology/rule"
)

// TestFlushCheckCount 证明缓冲区按 tag 有序、只看队头而非整表扫描：
// 不到 G 的版本检查个数不随 m 增长；到 G 的版本不超过刷出条数加常数。
func TestFlushCheckCount(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprint(m), func(t *testing.T) {
			const G = 5
			in := New(0, m)
			for i := 0; i < m; i++ {
				if _, ok := in.Accept(rule.Data{Key: int64(i), Val: 1, Tag: G}); !ok {
					t.Fatal("不应拒绝")
				}
			}
			for v := 1; v < G; v++ { // 投递不到 G 的版本
				in.Apply(rule.Put("r", 0))
				if in.checked > 2 {
					t.Fatalf("m=%d v=%d: checked=%d 随 m 线性增长", m, v, in.checked)
				}
			}
			in.Apply(rule.Put("r", 0)) // 投递到 G
			if in.checked > m+1 {
				t.Fatalf("checked=%d 超过刷出条数 %d 加常数", in.checked, m)
			}
			if in.Buffered() != 0 {
				t.Fatal("未刷空")
			}
		})
	}
}

// TestAccept 核验立即处理、缓冲上限拒绝与按 tag 有序。
func TestAccept(t *testing.T) {
	for _, tc := range []struct{ max, sends, wantBuf int }{{1, 3, 1}, {3, 3, 3}, {0, 2, 0}} {
		in := New(0, tc.max)
		in.Apply(rule.Put("r", 10)) // v=1
		if h, ok := in.Accept(rule.Data{Key: 1, Val: 15, Tag: 1}); !ok || len(h) != 1 {
			t.Fatal("tag==v 应立即处理")
		}
		got := 0
		for i := 0; i < tc.sends; i++ {
			if _, ok := in.Accept(rule.Data{Key: int64(i), Val: 1, Tag: 2}); ok {
				got++
			}
		}
		if got != tc.wantBuf || in.Buffered() != tc.wantBuf {
			t.Fatalf("max=%d: 缓冲 %d, want %d", tc.max, got, tc.wantBuf)
		}
		if err := in.CheckAgainst(2); err != nil {
			t.Fatal(err)
		}
	}
}
