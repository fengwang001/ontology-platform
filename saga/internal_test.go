package saga

import (
	"testing"

	"ontology/effect"
	"ontology/journal"
)

// TestRecoverReadCount 验证 Recover 单次读取记录数等于该实例日志
// 条数，不随其他实例的记录总数增长（非导出计数器 lastReads）。
func TestRecoverReadCount(t *testing.T) {
	for _, others := range []int{1, 10000} {
		jr, ef := journal.New(), effect.New()
		m := New(jr, ef)
		if err := m.Begin("target", 3, 1, 1); err != nil {
			t.Fatal(err)
		}
		if err := m.Intent("target", 0); err != nil {
			t.Fatal(err)
		}
		if err := m.Done("target", 0); err != nil {
			t.Fatal(err)
		}
		// 其他实例制造大量记录。
		if err := m.Begin("other", 1, 0, 0); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < others; i++ {
			jr.Append("other", journal.Record{Kind: journal.KindFail, Step: 0, Fail: journal.Transient})
		}
		if _, err := m.Recover("target"); err != nil {
			t.Fatal(err)
		}
		got := m.lastReads.Load()
		want := int64(jr.Len("target"))
		t.Logf("其他实例记录=%d: Recover 读取=%d, 目标实例日志=%d", others, got, want)
		if got != want {
			t.Fatalf("其他实例 %d 条记录时读取 %d, 期望 %d", others, got, want)
		}
	}
}
