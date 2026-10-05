package corpact

import (
	"errors"
	"testing"

	"ontology/holding"
)

func newReg() (*Registry, *holding.Book) {
	b := holding.NewBook()
	return New(b), b
}

func TestAnnounceValidationOrder(t *testing.T) {
	r, _ := newReg()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(r.SetClose("S", 1000))

	cases := []struct {
		name string
		id   string
		sym  string
		c, b int64
		rec  int
		ex   int
		want error
	}{
		{"空id", "", "S", 1, 1, 1, 2, holding.ErrInvalidParam},
		{"空标的", "a", "", 1, 1, 1, 2, holding.ErrInvalidParam},
		{"c为负", "a", "S", -1, 1, 1, 2, holding.ErrInvalidParam},
		{"c超限", "a", "S", MaxCash + 1, 1, 1, 2, holding.ErrInvalidParam},
		{"b为负", "a", "S", 1, -1, 1, 2, holding.ErrInvalidParam},
		{"b超限", "a", "S", 1, MaxBonus + 1, 1, 2, holding.ErrInvalidParam},
		{"c与b同时为0", "a", "S", 0, 0, 1, 2, holding.ErrInvalidParam},
		{"rec为负", "a", "S", 1, 1, -1, 2, holding.ErrInvalidParam},
		{"rec不小于ex", "a", "S", 1, 1, 2, 2, holding.ErrInvalidParam},
		{"ex超限", "a", "S", 1, 1, 1, MaxDay + 1, holding.ErrInvalidParam},
		{"无参考价", "a", "T", 1, 1, 1, 2, holding.ErrNoRefPrice},
		{"正常", "a", "S", 1, 1, 1, 2, nil},
		{"编号重复优先于状态不符", "a", "S", 1, 1, 0, 1, holding.ErrDuplicate},
		{"同标的冲突", "b", "S", 1, 1, 1, 2, holding.ErrConflict},
	}
	for _, tc := range cases {
		err := r.Announce(tc.id, tc.sym, tc.c, tc.b, tc.rec, tc.ex)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: want %v, got %v", tc.name, tc.want, err)
		}
	}

	// 状态不符优先于冲突：推进到 rec 之后，对已有行动的标的再登记
	if _, err := r.Advance(1); err != nil {
		t.Fatal(err)
	}
	if err := r.Announce("c", "S", 1, 1, 1, 2); !errors.Is(err, holding.ErrState) {
		t.Fatalf("want ErrState, got %v", err)
	}
	// 冲突优先于无参考价的场景无法构造（有行动必有收盘价），
	// 此处验证冲突在独立情形下报出即可（上面“同标的冲突”）。
}

func TestSetCloseValidation(t *testing.T) {
	r, _ := newReg()
	for _, tc := range []struct {
		sym  string
		p    int64
		want error
	}{
		{"", 1, holding.ErrInvalidParam},
		{"S", 0, holding.ErrInvalidParam},
		{"S", MaxClose + 1, holding.ErrInvalidParam},
		{"S", 1, nil},
		{"S", MaxClose, nil},
	} {
		if err := r.SetClose(tc.sym, tc.p); !errors.Is(err, tc.want) {
			t.Errorf("SetClose(%q,%d): want %v, got %v", tc.sym, tc.p, tc.want, err)
		}
	}
	if p, ok := r.Close("S"); !ok || p != MaxClose {
		t.Fatalf("close = %d,%v", p, ok)
	}
}

func TestAdvanceValidation(t *testing.T) {
	r, _ := newReg()
	if _, err := r.Advance(-1); !errors.Is(err, holding.ErrInvalidParam) {
		t.Fatalf("want ErrInvalidParam, got %v", err)
	}
	if _, err := r.Advance(MaxDay + 1); !errors.Is(err, holding.ErrInvalidParam) {
		t.Fatalf("want ErrInvalidParam, got %v", err)
	}
	if _, err := r.Advance(5); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Advance(4); !errors.Is(err, holding.ErrDateRollback) {
		t.Fatalf("want ErrDateRollback, got %v", err)
	}
	// 参数非法优先于日期回退
	if _, err := r.Advance(-2); !errors.Is(err, holding.ErrInvalidParam) {
		t.Fatalf("want ErrInvalidParam, got %v", err)
	}
	// 推进到当日为合法空操作
	if _, err := r.Advance(5); err != nil {
		t.Fatal(err)
	}
	if r.Day() != 5 {
		t.Fatalf("day = %d", r.Day())
	}
}

// 快照时机：当前日首次大于 rec 时采集 rec 日末状态。
func TestSnapshotTiming(t *testing.T) {
	r, b := newReg()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(b.Trade("A", "S", 100))
	must(r.SetClose("S", 500))
	must(r.Announce("act", "S", 10, 5, 2, 4))

	// day=1：未越过 rec，无快照
	exec, err := r.Advance(1)
	must(err)
	if len(exec) != 0 {
		t.Fatalf("exec = %v", exec)
	}
	must(b.Trade("A", "S", 50)) // day1 末买入，应入快照
	// day=2：等于 rec 仍无快照
	exec, err = r.Advance(2)
	must(err)
	if len(exec) != 0 {
		t.Fatalf("exec = %v", exec)
	}
	must(b.Trade("A", "S", 30)) // day2（rec 当日）末买入，应入快照
	// day=3：首次大于 rec，拍快照
	exec, err = r.Advance(3)
	must(err)
	if len(exec) != 0 {
		t.Fatalf("exec = %v", exec)
	}
	act, _ := r.GetAction("act")
	if !act.Snapshotted {
		t.Fatal("should be snapshotted")
	}
	if got := act.Snapshot["A"]; got.Q != 180 {
		t.Fatalf("snapshot q = %d, want 180", got.Q)
	}
	must(b.Trade("A", "S", 1000)) // 快照后买入不影响
	// day=4：到达 ex，返回待执行
	exec, err = r.Advance(4)
	must(err)
	if len(exec) != 1 || exec[0] != "act" {
		t.Fatalf("exec = %v", exec)
	}
}

// 一次推进同时越过 rec 与 ex：先快照后执行（快照取 rec 日末状态）。
func TestAdvanceCrossesBoth(t *testing.T) {
	r, b := newReg()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(b.Trade("A", "S", 100))
	must(r.SetClose("S", 500))
	must(r.Announce("act", "S", 10, 5, 5, 7))
	must(b.Trade("A", "S", 1)) // day0 末共 101 股
	exec, err := r.Advance(7)
	must(err)
	if len(exec) != 1 || exec[0] != "act" {
		t.Fatalf("exec = %v", exec)
	}
	act, _ := r.GetAction("act")
	if !act.Snapshotted || act.Snapshot["A"].Q != 101 {
		t.Fatalf("snapshot = %+v", act.Snapshot["A"])
	}
}

func TestCancelAction(t *testing.T) {
	r, _ := newReg()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := r.CancelAction(""); !errors.Is(err, holding.ErrInvalidParam) {
		t.Fatalf("want ErrInvalidParam, got %v", err)
	}
	if err := r.CancelAction("x"); !errors.Is(err, holding.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
	must(r.SetClose("S", 100))
	must(r.Announce("a", "S", 1, 1, 1, 3))
	must(r.CancelAction("a"))
	// 撤销后同标的可再登记
	must(r.Announce("b", "S", 1, 1, 1, 3))
	// 快照后不可撤销
	if _, err := r.Advance(2); err != nil {
		t.Fatal(err)
	}
	if err := r.CancelAction("b"); !errors.Is(err, holding.ErrState) {
		t.Fatalf("want ErrState, got %v", err)
	}
}

func TestResultAndRecordResult(t *testing.T) {
	r, _ := newReg()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Result(""); !errors.Is(err, holding.ErrInvalidParam) {
		t.Fatalf("want ErrInvalidParam, got %v", err)
	}
	if _, err := r.Result("x"); !errors.Is(err, holding.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
	must(r.SetClose("S", 1000))
	must(r.Announce("a", "S", 30, 3, 1, 2))
	if _, err := r.Result("a"); !errors.Is(err, holding.ErrState) {
		t.Fatalf("want ErrState, got %v", err)
	}
	if _, err := r.Advance(2); err != nil {
		t.Fatal(err)
	}
	res := &Result{Pex: 767}
	r.RecordResult("a", res)
	got, err := r.Result("a")
	must(err)
	if got.Pex != 767 {
		t.Fatalf("pex = %d", got.Pex)
	}
	// 执行后收盘价记为 Pex
	if p, ok := r.Close("S"); !ok || p != 767 {
		t.Fatalf("close = %d,%v", p, ok)
	}
	// 执行后同标的可再登记新行动
	must(r.Announce("b", "S", 1, 1, 3, 4))
}
