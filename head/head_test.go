package head_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/head"
)

func mustNew(t *testing.T, l, ooo int64, smax, lim int) *head.Head {
	t.Helper()
	h, err := head.New(l, ooo, smax, lim)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h
}

func TestNewInvalidParams(t *testing.T) {
	bad := [][4]int64{{0, 0, 1, 1}, {1e9 + 1, 0, 1, 1}, {1, -1, 1, 1}, {1, 1e9 + 1, 1, 1},
		{1, 0, 0, 1}, {1, 0, 1e6 + 1, 1}, {1, 0, 1, 0}, {1, 0, 1, 1e5 + 1}}
	for _, p := range bad {
		if _, err := head.New(p[0], p[1], int(p[2]), int(p[3])); !errors.Is(err, head.ErrInvalid) {
			t.Fatalf("New%v: 期望 ErrInvalid, 得到 %v", p, err)
		}
	}
}

// 乱序窗口：恰等 Ooo 接受、差 1 过旧；同 ts 重复与冲突。
func TestAppendOutOfOrder(t *testing.T) {
	h := mustNew(t, 300, 100, 10, 10)
	step := func(name string, ts, v int64, want error) {
		err := h.Append(name, ts, v)
		if !errors.Is(err, want) {
			t.Fatalf("Append(%q,%d,%d) = %v, 期望 %v", name, ts, v, err, want)
		}
		t.Logf("输入 Append(%q,%d,%d) 输出 %v 判定 %v", name, ts, v, err, want)
	}
	step("x", 1000, 1, nil)
	step("x", 950, 2, nil)            // 差 50 ≤ Ooo
	step("x", 900, 3, nil)            // 差恰等 Ooo=100, 接受
	step("x", 899, 4, head.ErrTooOld) // 差 101 > Ooo
	step("x", 890, 5, head.ErrTooOld) // 差 110 > Ooo
	step("x", 1000, 1, nil)           // 重复
	if h.Dups != 1 {
		t.Fatalf("Dups = %d, 期望 1", h.Dups)
	}
	step("x", 1000, 2, head.ErrConflict) // 同 ts 不同值
	step("x", 1001, 9, nil)              // 新的最大 ts
	step("x", 901, 6, nil)               // 1001-901=100 恰等 Ooo, 接受
	step("x", 888, 7, head.ErrTooOld)    // 1001-888=113 > Ooo
}

// 陈旧标记与同 ts 活样本冲突；标记与标记同 ts 为重复。
func TestStaleMarkerConflicts(t *testing.T) {
	h := mustNew(t, 300, 100, 10, 10)
	if err := h.Append("s", 100, 1); err != nil {
		t.Fatal(err)
	}
	if err := h.AppendStale("s", 100); !errors.Is(err, head.ErrConflict) {
		t.Fatalf("活样本上写标记 = %v, 期望 ErrConflict", err)
	}
	if err := h.AppendStale("s", 200); err != nil {
		t.Fatal(err)
	}
	if err := h.Append("s", 200, 1); !errors.Is(err, head.ErrConflict) {
		t.Fatalf("标记上写活样本 = %v, 期望 ErrConflict", err)
	}
	if err := h.AppendStale("s", 200); err != nil || h.Dups != 1 {
		t.Fatalf("重复标记 err=%v Dups=%d, 期望 nil/1", err, h.Dups)
	}
	t.Logf("判定: 标记与活样本同 ts 互相冲突; 同 ts 同种类同值为重复(Dups=1)")
}

// 序列上限：新序列被拒, 已有序列仍可写; 批量内新序列累计计入。
func TestSeriesLimit(t *testing.T) {
	h := mustNew(t, 300, 100, 2, 10)
	for _, name := range []string{"a", "b"} {
		if err := h.Append(name, 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.Append("c", 1, 1); !errors.Is(err, head.ErrTooManySeries) {
		t.Fatalf("第 3 个序列 = %v, 期望 ErrTooManySeries", err)
	}
	if err := h.Append("a", 2, 1); err != nil {
		t.Fatalf("已有序列追加 = %v, 期望 nil", err)
	}
	err := h.ApplyBatch([]head.Item{{Series: "c", Ts: 1}, {Series: "d", Ts: 1}})
	if !errors.Is(err, head.ErrTooManySeries) {
		t.Fatalf("批量累计超限 = %v, 期望 ErrTooManySeries", err)
	}
	if _, ok := h.SeriesSamples("c"); ok {
		t.Fatal("批量失败后 c 不应存在")
	}
}

// 拒绝顺序：参数非法 > 序列上限 > 冲突 > 过旧。
func TestRejectionOrder(t *testing.T) {
	h := mustNew(t, 300, 100, 1, 10)
	if err := h.Append("a", 1000, 1); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("n", 129)
	if err := h.Append(long, 1, 1); !errors.Is(err, head.ErrInvalid) {
		t.Fatal("非法名字应报 ErrInvalid")
	}
	if err := h.Append("b", -1, 1); !errors.Is(err, head.ErrInvalid) {
		t.Fatal("非法 ts 应报 ErrInvalid (先于序列上限)")
	}
	if err := h.Append("b", 1, 1); !errors.Is(err, head.ErrTooManySeries) {
		t.Fatal("新序列应报 ErrTooManySeries")
	}
	if err := h.Append("a", 1000, 2); !errors.Is(err, head.ErrConflict) {
		t.Fatal("同 ts 不同值应报 ErrConflict (先于过旧)")
	}
	if err := h.Append("a", 800, 1); !errors.Is(err, head.ErrTooOld) {
		t.Fatal("窗口外应报 ErrTooOld")
	}
}

// ApplyBatch 原子性：任一项失败则整批不写。
func TestBatchAllOrNothing(t *testing.T) {
	h := mustNew(t, 300, 100, 10, 10)
	if err := h.Append("x", 1000, 1); err != nil {
		t.Fatal(err)
	}
	err := h.ApplyBatch([]head.Item{
		{Series: "y", Ts: 1000, V: 2},
		{Series: "x", Ts: 800, V: 3}, // 过旧
	})
	if !errors.Is(err, head.ErrTooOld) {
		t.Fatalf("err = %v, 期望 ErrTooOld", err)
	}
	if _, ok := h.SeriesSamples("y"); ok {
		t.Fatal("整批失败时 y 不应写入")
	}
}
