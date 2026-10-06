package revenue_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/revenue"
)

func mustBook(t *testing.T, wd int64) *revenue.Book {
	t.Helper()
	b, err := revenue.NewBook(wd)
	if err != nil {
		t.Fatalf("NewBook(%d): %v", wd, err)
	}
	return b
}

func TestNewBookValidation(t *testing.T) {
	for _, wd := range []int64{-1, 1_000_000_001} {
		if _, err := revenue.NewBook(wd); !errors.Is(err, revenue.ErrInvalidParam) {
			t.Fatalf("NewBook(%d) err=%v, want ErrInvalidParam", wd, err)
		}
	}
	for _, wd := range []int64{0, 1, 1_000_000_000} {
		if _, err := revenue.NewBook(wd); err != nil {
			t.Fatalf("NewBook(%d): %v", wd, err)
		}
	}
}

func TestSetSplitValidation(t *testing.T) {
	dup := []revenue.Part{{Creator: "a", BPS: 5000}, {Creator: "a", BPS: 5000}}
	sumBad := []revenue.Part{{Creator: "a", BPS: 5000}, {Creator: "b", BPS: 5001}}
	nine := make([]revenue.Part, 9)
	for i := range nine {
		nine[i] = revenue.Part{Creator: fmt.Sprintf("c%d", i), BPS: 1000}
	}
	nine[8].BPS = 2000
	cases := []struct {
		name    string
		now     int64
		content string
		parts   []revenue.Part
	}{
		{"now为负", -1, "c", []revenue.Part{{Creator: "a", BPS: 10000}}},
		{"now超界", 1_000_000_000_001, "c", []revenue.Part{{Creator: "a", BPS: 10000}}},
		{"内容为空", 0, "", []revenue.Part{{Creator: "a", BPS: 10000}}},
		{"零项", 0, "c", nil},
		{"九项", 0, "c", nine},
		{"创作者为空", 0, "c", []revenue.Part{{Creator: "", BPS: 10000}}},
		{"基点为零", 0, "c", []revenue.Part{{Creator: "a", BPS: 0}}},
		{"基点超界", 0, "c", []revenue.Part{{Creator: "a", BPS: 10001}}},
		{"基点为负", 0, "c", []revenue.Part{{Creator: "a", BPS: -5}}},
		{"创作者重复", 0, "c", dup},
		{"合计不为10000", 0, "c", sumBad},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := mustBook(t, 0)
			err := b.SetSplit(tc.now, tc.content, tc.parts)
			if !errors.Is(err, revenue.ErrInvalidParam) {
				t.Fatalf("SetSplit err=%v, want ErrInvalidParam", err)
			}
		})
	}
}

func TestSetSplitValidAndClockOrder(t *testing.T) {
	b := mustBook(t, 0)
	good := []revenue.Part{{Creator: "a", BPS: 7000}, {Creator: "b", BPS: 3000}}
	if err := b.SetSplit(100, "c", good); err != nil {
		t.Fatalf("SetSplit: %v", err)
	}
	// 参数非法优先于时钟回退
	err := b.SetSplit(50, "c", []revenue.Part{{Creator: "a", BPS: 1}})
	if !errors.Is(err, revenue.ErrInvalidParam) {
		t.Fatalf("err=%v, want ErrInvalidParam", err)
	}
	// 时钟回退
	if err := b.SetSplit(50, "c", good); !errors.Is(err, revenue.ErrClockRewind) {
		t.Fatalf("err=%v, want ErrClockRewind", err)
	}
	// 被拒不改时钟：t=100 仍被接受
	if err := b.SetSplit(100, "c", good); err != nil {
		t.Fatalf("SetSplit at same now: %v", err)
	}
}

func TestEarnSharesAndRemainder(t *testing.T) {
	b := mustBook(t, 100)
	parts := []revenue.Part{
		{Creator: "u1", BPS: 3333},
		{Creator: "u2", BPS: 3333},
		{Creator: "u3", BPS: 3334},
	}
	if err := b.SetSplit(0, "c", parts); err != nil {
		t.Fatalf("SetSplit: %v", err)
	}
	cases := []struct {
		amount int64
		want   []int64
	}{
		{10000, []int64{3333, 3333, 3334}},
		{1, []int64{1, 0, 0}},             // 全部取整为 0，余 1 归第一位
		{9999, []int64{3334, 3332, 3333}}, // 余 2 归第一位
		{3, []int64{2, 0, 1}},             // u3 取整得 1，余 2 归第一位
	}
	for i, tc := range cases {
		got, err := b.Earn(int64(i+1), fmt.Sprintf("e%d", i), "c", tc.amount)
		if err != nil {
			t.Fatalf("Earn(%d): %v", tc.amount, err)
		}
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Fatalf("Earn(%d)=%v, want %v", tc.amount, got, tc.want)
		}
		var sum int64
		for _, s := range got {
			sum += s
		}
		if sum != tc.amount {
			t.Fatalf("份额之和 %d != amount %d", sum, tc.amount)
		}
	}
}

func TestEarnValidation(t *testing.T) {
	b := mustBook(t, 0)
	_ = b.SetSplit(0, "c", []revenue.Part{{Creator: "a", BPS: 10000}})
	cases := []struct {
		name    string
		now     int64
		id      string
		content string
		amount  int64
	}{
		{"now为负", -1, "e", "c", 1},
		{"now超界", 1_000_000_000_001, "e", "c", 1},
		{"事件ID为空", 1, "", "c", 1},
		{"内容为空", 1, "e", "", 1},
		{"金额为零", 1, "e", "c", 0},
		{"金额为负", 1, "e", "c", -1},
		{"金额超界", 1, "e", "c", 1_000_000_000_001},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := b.Earn(tc.now, tc.id, tc.content, tc.amount)
			if !errors.Is(err, revenue.ErrInvalidParam) {
				t.Fatalf("err=%v, want ErrInvalidParam", err)
			}
		})
	}
}

func TestEarnRejectionOrder(t *testing.T) {
	b := mustBook(t, 0)
	_ = b.SetSplit(10, "c", []revenue.Part{{Creator: "a", BPS: 10000}})
	if _, err := b.Earn(20, "e1", "c", 100); err != nil {
		t.Fatalf("Earn: %v", err)
	}
	// 参数非法 > 时钟回退
	if _, err := b.Earn(5, "e2", "c", 0); !errors.Is(err, revenue.ErrInvalidParam) {
		t.Fatalf("err=%v, want ErrInvalidParam", err)
	}
	// 时钟回退 > 内容无分成表
	if _, err := b.Earn(5, "e2", "nosplit", 100); !errors.Is(err, revenue.ErrClockRewind) {
		t.Fatalf("err=%v, want ErrClockRewind", err)
	}
	// 内容无分成表 > 事件冲突（e1 已存在但内容无表先报）
	if _, err := b.Earn(30, "e1", "nosplit", 100); !errors.Is(err, revenue.ErrNoSplit) {
		t.Fatalf("err=%v, want ErrNoSplit", err)
	}
	// 事件冲突：金额不同
	if _, err := b.Earn(30, "e1", "c", 101); !errors.Is(err, revenue.ErrEventConflict) {
		t.Fatalf("err=%v, want ErrEventConflict", err)
	}
	// 事件冲突：内容不同（先给 d 建表）
	_ = b.SetSplit(30, "d", []revenue.Part{{Creator: "a", BPS: 10000}})
	if _, err := b.Earn(40, "e1", "d", 100); !errors.Is(err, revenue.ErrEventConflict) {
		t.Fatalf("err=%v, want ErrEventConflict", err)
	}
	// 被拒不改状态含时钟：t=50 的冲突被拒后，t=45 的操作仍被接受
	if _, err := b.Earn(50, "e1", "c", 102); !errors.Is(err, revenue.ErrEventConflict) {
		t.Fatalf("err=%v, want ErrEventConflict", err)
	}
	if _, err := b.Earn(45, "e2", "c", 5); err != nil {
		t.Fatalf("Earn e2 after rejections: %v", err)
	}
}

func TestEarnIdempotent(t *testing.T) {
	b := mustBook(t, 0)
	parts := []revenue.Part{{Creator: "a", BPS: 7000}, {Creator: "b", BPS: 3000}}
	_ = b.SetSplit(0, "c", parts)
	first, err := b.Earn(10, "e1", "c", 999)
	if err != nil {
		t.Fatalf("Earn: %v", err)
	}
	// 重复且 content 与 amount 都相同：空操作，返回原份额
	second, err := b.Earn(20, "e1", "c", 999)
	if err != nil {
		t.Fatalf("idempotent Earn: %v", err)
	}
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("重放返回 %v, want %v", second, first)
	}
	// 重放不改变账本：创作者可用余额仍只有一份
	c, _ := b.GetCreator("a")
	c.Promote(1000)
	if c.Available() != first[0] {
		t.Fatalf("available=%d, want %d（重放不应重复入账）", c.Available(), first[0])
	}
}

func TestEarnSplitOnlyAffectsLaterEvents(t *testing.T) {
	b := mustBook(t, 0)
	_ = b.SetSplit(0, "c", []revenue.Part{{Creator: "a", BPS: 5000}, {Creator: "b", BPS: 5000}})
	got1, _ := b.Earn(1, "e1", "c", 100)
	// 换表只影响其后的收入事件
	_ = b.SetSplit(2, "c", []revenue.Part{{Creator: "a", BPS: 10000}})
	got2, _ := b.Earn(3, "e2", "c", 100)
	if fmt.Sprint(got1) != "[50 50]" || fmt.Sprint(got2) != "[100]" {
		t.Fatalf("got1=%v got2=%v", got1, got2)
	}
	// 重放 e1 仍返回旧份额
	replay, _ := b.Earn(4, "e1", "c", 100)
	if fmt.Sprint(replay) != "[50 50]" {
		t.Fatalf("replay=%v", replay)
	}
}

func TestEventShareSumInvariant(t *testing.T) {
	b := mustBook(t, 0)
	parts := []revenue.Part{
		{Creator: "a", BPS: 1234}, {Creator: "b", BPS: 2345},
		{Creator: "c", BPS: 3333}, {Creator: "d", BPS: 3088},
	}
	_ = b.SetSplit(0, "c", parts)
	amounts := []int64{1, 2, 7, 99, 100, 999, 12345, 999_999_999_999, 1_000_000_000_000}
	for i, amt := range amounts {
		got, err := b.Earn(int64(i+1), fmt.Sprintf("e%d", i), "c", amt)
		if err != nil {
			t.Fatalf("Earn(%d): %v", amt, err)
		}
		var sum int64
		for _, s := range got {
			sum += s
		}
		if sum != amt {
			t.Fatalf("amount=%d 份额之和=%d", amt, sum)
		}
	}
}
