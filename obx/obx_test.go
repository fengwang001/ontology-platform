package obx

import (
	"errors"
	"slices"
	"testing"
)

// 取批走按提交序维护的待投队列，而非整表扫描：
// 发件箱里已有 m 条已标记消息时，再提交 1 条并取批，
// 检查条数不随 m 线性增长（小常数 + 本次真正投递的条数）。
func TestTakeScanFree(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		b := New(m + 1)
		for i := 0; i < m; i++ {
			if err := b.Write("bulk", "p"); err != nil {
				t.Fatal(err)
			}
		}
		if err := b.Commit("bulk"); err != nil {
			t.Fatal(err)
		}
		for _, msg := range b.Take() {
			b.Mark(msg.ID)
		}
		if len(b.rows) != m {
			t.Fatalf("m=%d: rows=%d", m, len(b.rows))
		}
		if err := b.Write("new", "p"); err != nil {
			t.Fatal(err)
		}
		if err := b.Commit("new"); err != nil {
			t.Fatal(err)
		}
		got := b.Take()
		if len(got) != 1 || got[0].ID != m+1 {
			t.Fatalf("m=%d: got %v", m, got)
		}
		if b.checked > 2 { // 小常数 + 本次真正投递的 1 条
			t.Fatalf("m=%d: checked=%d grows with table size", m, b.checked)
		}
	}
}

// 基本语义：id/csn 分配、中止丢弃且 id 不回收、(csn,id) 有序取批、
// 被拒的提交不消耗 csn、事务保持开启可继续写入。
func TestObxBasics(t *testing.T) {
	b := New(4)
	b.Write("a", "x") // id1
	b.Write("b", "x") // id2，将被中止
	b.Write("a", "x") // id3
	b.Abort("b")
	b.Commit("a") // csn1
	b.Write("c", "x")
	b.Commit("c") // csn2
	want := []Msg{{1, 1, "x"}, {3, 1, "x"}, {4, 2, "x"}}
	if got := b.Take(); !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	b.Write("d", "x")
	b.Write("d", "x")
	if err := b.Commit("d"); !errors.Is(err, ErrBacklog) {
		t.Fatalf("want ErrBacklog, got %v", err)
	}
	if b.nextCSN != 2 || b.nextID != 6 {
		t.Fatalf("rejected commit consumed counters: csn=%d id=%d", b.nextCSN, b.nextID)
	}
	if err := b.Commit("d"); !errors.Is(err, ErrBacklog) { // 仍超限，d 保持开启
		t.Fatal(err)
	}
	b.Mark(1)
	b.Mark(3)
	b.Mark(4)
	if err := b.Commit("d"); err != nil { // 腾出空间后成功，csn=3
		t.Fatal(err)
	}
	if got := b.Take(); len(got) != 2 || got[0].CSN != 3 || got[1].CSN != 3 {
		t.Fatalf("got %v", got)
	}
}
