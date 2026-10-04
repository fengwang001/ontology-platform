package count

import (
	"errors"
	"fmt"
	"testing"

	"ontology/adjust"
)

func newFixture(t *testing.T) (*Engine, *adjust.DB) {
	t.Helper()
	db := adjust.New(2, 5, 500)
	if err := db.AddLoc([]byte("X"), 100, 10); err != nil {
		t.Fatal(err)
	}
	if err := db.AddLoc([]byte("Y"), 200, 10); err != nil {
		t.Fatal(err)
	}
	return New(db), db
}

func TestFirstTolerance(t *testing.T) {
	cases := []struct {
		name      string
		counted   int64
		wantPhase Phase
		wantBook  int64
	}{
		{"within pct", 97, Done, 97},   // diff=-3, tol=5
		{"equal tol", 95, Done, 95},    // diff=-5 恰等
		{"below tol", 94, Second, 100}, // diff=-6
		{"within abs", 102, Done, 102}, // +2 恰等绝对容差
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, db := newFixture(t)
			if err := e.Open([]byte("t1"), [][]byte{[]byte("X")}); err != nil {
				t.Fatal(err)
			}
			if err := e.Submit([]byte("t1"), []byte("X"), tc.counted, []byte("u1")); err != nil {
				t.Fatal(err)
			}
			ph, err := e.Phase([]byte("t1"), []byte("X"))
			if err != nil || ph != tc.wantPhase {
				t.Fatalf("phase=%v err=%v, want %v", ph, err, tc.wantPhase)
			}
			book, _ := db.Book([]byte("X"))
			if book != tc.wantBook {
				t.Fatalf("book=%d want %d", book, tc.wantBook)
			}
		})
	}
}

func TestExampleConsistency(t *testing.T) {
	// 题目例 Y：180 初盘超差 -> Move -30 -> u2 提交 150，
	// 两次实盘差 -30 恰等净移动 -30，差值为本次 diff=-20。
	e, db := newFixture(t)
	if err := e.Open([]byte("t"), [][]byte{[]byte("Y")}); err != nil {
		t.Fatal(err)
	}
	if err := e.Submit([]byte("t"), []byte("Y"), 180, []byte("u1")); err != nil {
		t.Fatal(err)
	}
	if err := db.Move([]byte("Y"), -30); err != nil {
		t.Fatal(err)
	}
	if err := e.Submit([]byte("t"), []byte("Y"), 180, []byte("u1")); !errors.Is(err, ErrRotate) {
		t.Fatalf("same counter second round: %v", err)
	}
	if err := e.Submit([]byte("t"), []byte("Y"), 150, []byte("u2")); err != nil {
		t.Fatal(err)
	}
	ph, _ := e.Phase([]byte("t"), []byte("Y"))
	if ph != Pending {
		t.Fatalf("phase=%v want Pending", ph)
	}
	d, _ := e.Diff([]byte("t"), []byte("Y"))
	if d != -20 {
		t.Fatalf("diff=%d want -20", d)
	}
}

func TestExampleInconsistentAndThird(t *testing.T) {
	// u2 提交 152：152-180=-28 != -30，进入 Third；u3 提交 151，超差，Pending(-19)。
	e, _ := newFixture(t)
	if err := e.Open([]byte("t"), [][]byte{[]byte("Y")}); err != nil {
		t.Fatal(err)
	}
	_ = e.Submit([]byte("t"), []byte("Y"), 180, []byte("u1"))
	_ = moveY(e, -30)
	if err := e.Submit([]byte("t"), []byte("Y"), 152, []byte("u2")); err != nil {
		t.Fatal(err)
	}
	if ph, _ := e.Phase([]byte("t"), []byte("Y")); ph != Third {
		t.Fatalf("phase=%v want Third", ph)
	}
	if err := e.Submit([]byte("t"), []byte("Y"), 151, []byte("u3")); err != nil {
		t.Fatal(err)
	}
	if ph, _ := e.Phase([]byte("t"), []byte("Y")); ph != Pending {
		t.Fatalf("phase=%v want Pending", ph)
	}
	d, _ := e.Diff([]byte("t"), []byte("Y"))
	if d != -19 {
		t.Fatalf("diff=%d want -19", d)
	}
	// Third 提交人不得与前两人相同。
}

func moveY(e *Engine, delta int64) error {
	return e.db.Move([]byte("Y"), delta)
}

func TestSecondWithinAfterMove(t *testing.T) {
	// u2 提交 165：book=170, tol=8, diff=-5 容差内，直接采纳 book=165。
	e, db := newFixture(t)
	_ = e.Open([]byte("t"), [][]byte{[]byte("Y")})
	_ = e.Submit([]byte("t"), []byte("Y"), 180, []byte("u1"))
	_ = db.Move([]byte("Y"), -30)
	if err := e.Submit([]byte("t"), []byte("Y"), 165, []byte("u2")); err != nil {
		t.Fatal(err)
	}
	if ph, _ := e.Phase([]byte("t"), []byte("Y")); ph != Done {
		t.Fatalf("phase=%v want Done", ph)
	}
	book, _ := db.Book([]byte("Y"))
	if book != 165 {
		t.Fatalf("book=%d want 165", book)
	}
}

func TestThirdRotate(t *testing.T) {
	e, _ := newFixture(t)
	_ = e.Open([]byte("t"), [][]byte{[]byte("Y")})
	_ = e.Submit([]byte("t"), []byte("Y"), 180, []byte("u1"))
	_ = moveY(e, -30)
	_ = e.Submit([]byte("t"), []byte("Y"), 152, []byte("u2"))
	for _, same := range [][]byte{[]byte("u1"), []byte("u2")} {
		if err := e.Submit([]byte("t"), []byte("Y"), 0, same); !errors.Is(err, ErrRotate) {
			t.Fatalf("third round by %s: err=%v want ErrRotate", same, err)
		}
	}
}

func TestOpenConflicts(t *testing.T) {
	e, _ := newFixture(t)
	if err := e.Open([]byte("t1"), [][]byte{[]byte("X")}); err != nil {
		t.Fatal(err)
	}
	// 任一库位已在未关闭任务中 -> 整体拒绝，Y 不应被占用。
	if err := e.Open([]byte("t2"), [][]byte{[]byte("X"), []byte("Y")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v want conflict", err)
	}
	if err := e.Open([]byte("t3"), [][]byte{[]byte("Y")}); err != nil {
		t.Fatalf("Y must remain free: %v", err)
	}
	// 任务号重复
	if err := e.Open([]byte("t1"), [][]byte{[]byte("Y")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("dup task: %v", err)
	}
	// 参数：重复库位、空列表、不存在库位
	if err := e.Open([]byte("t4"), [][]byte{[]byte("X"), []byte("X")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("dup locs: %v", err)
	}
	if err := e.Open([]byte("t5"), nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty: %v", err)
	}
	if err := e.Open([]byte("t6"), [][]byte{[]byte("ZZ")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing loc: %v", err)
	}
}

func TestCloseAndReopen(t *testing.T) {
	e, _ := newFixture(t)
	_ = e.Open([]byte("t"), [][]byte{[]byte("X")})
	// 未 Done 不能关
	if err := e.Close([]byte("t")); !errors.Is(err, ErrState) {
		t.Fatalf("close early: %v", err)
	}
	_ = e.Submit([]byte("t"), []byte("X"), 100, []byte("u1"))
	if err := e.Close([]byte("t")); err != nil {
		t.Fatal(err)
	}
	// 关闭后不能再提交 / 再关
	if err := e.Submit([]byte("t"), []byte("X"), 100, []byte("u2")); !errors.Is(err, ErrState) {
		t.Fatalf("submit closed: %v", err)
	}
	if err := e.Close([]byte("t")); !errors.Is(err, ErrState) {
		t.Fatalf("double close: %v", err)
	}
	// 库位可进入新任务；旧任务号不能复用。
	if err := e.Open([]byte("t2"), [][]byte{[]byte("X")}); err != nil {
		t.Fatalf("reopen loc: %v", err)
	}
	if err := e.Open([]byte("t"), [][]byte{[]byte("X")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("reuse task id: %v", err)
	}
}

func TestRejectOrdering(t *testing.T) {
	// 拒绝次序：参数非法 > 不存在 > 状态不符。
	e, _ := newFixture(t)
	_ = e.Open([]byte("t"), [][]byte{[]byte("X")})
	_ = e.Submit([]byte("t"), []byte("X"), 94, []byte("u1")) // -> Second
	cases := []struct {
		name string
		err  error
		call func() error
	}{
		{"invalid beats notfound", ErrInvalid, func() error {
			return e.Submit(nil, []byte("ZZ"), 0, []byte("u2"))
		}},
		{"notfound beats state", ErrNotFound, func() error {
			return e.Submit([]byte("nope"), []byte("X"), 100, []byte("u2"))
		}},
		{"loc not in task", ErrNotFound, func() error {
			return e.Submit([]byte("t"), []byte("Y"), 200, []byte("u2"))
		}},
	}
	for _, tc := range cases {
		if err := tc.call(); !errors.Is(err, tc.err) {
			t.Fatalf("%s: %v want %v", tc.name, err, tc.err)
		}
	}
	// 状态不符：First 库位不能以 Third 规则提交（阶段本身接受，但重复人在
	// Second 才有意义）；Pending/Done 阶段再提交报状态不符。
	_ = e.Submit([]byte("t"), []byte("X"), 94, []byte("u2")) // Second, 不一致 -> Third
	_ = e.Submit([]byte("t"), []byte("X"), 94, []byte("u3")) // Third 超差 -> Pending
	if ph, _ := e.Phase([]byte("t"), []byte("X")); ph != Pending {
		t.Fatalf("setup phase=%v", ph)
	}
	if err := e.Submit([]byte("t"), []byte("X"), 94, []byte("u9")); !errors.Is(err, ErrState) {
		t.Fatalf("submit pending: %v", err)
	}
	if _, err := e.Diff([]byte("nope"), []byte("X")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("diff missing task: %v", err)
	}
}

func TestScannedIndependentOfMoveCount(t *testing.T) {
	for _, n := range []int{10, 10000} {
		t.Run(fmt.Sprintf("moves=%d", n), func(t *testing.T) {
			e, db := newFixture(t)
			_ = e.Open([]byte("t"), [][]byte{[]byte("Y")})
			_ = e.Submit([]byte("t"), []byte("Y"), 180, []byte("u1"))
			// n 笔被接受的 Move（交替 +1/-1，保持 book 足够）。
			net := int64(0)
			for i := 0; i < n; i++ {
				d := int64(1)
				if i%2 == 0 {
					d = -1
				}
				if err := db.Move([]byte("Y"), d); err != nil {
					t.Fatal(err)
				}
				net += d
			}
			e.scanned = 0
			// c2 = c1 + net，恰被净移动解释 -> Pending。
			if err := e.Submit([]byte("t"), []byte("Y"), 180+net, []byte("u2")); err != nil {
				t.Fatal(err)
			}
			if e.scanned != 0 {
				t.Fatalf("scanned=%d want 0 (moves=%d)", e.scanned, n)
			}
			if ph, _ := e.Phase([]byte("t"), []byte("Y")); ph != Pending {
				t.Fatalf("phase=%v want Pending", ph)
			}
		})
	}
}
