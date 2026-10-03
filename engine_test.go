package ontology

import (
	"errors"
	"testing"
)

func testDims() map[string]int {
	return map[string]int{"d1": 1, "d2": 1, "d3": 3}
}

func testRoles() map[string]int {
	return map[string]int{"r1": 1, "r2": 2, "r3": 3}
}

func mkev(ts int64, pairs ...string) Event {
	return Event{Ts: ts, Dims: map[string]string{
		"d1": pairs[0], "d2": pairs[1], "d3": pairs[2],
	}}
}

func newTestEngine(cMax, eMax int) *Engine {
	return New(testDims(), testRoles(), []int{3, 5, 7}, cMax, eMax)
}

func TestHalfOpenRangeAndUnorderedAppend(t *testing.T) {
	e := newTestEngine(10000, 100000)
	batch := []Event{
		mkev(10, "a", "x", "z"), mkev(10, "a", "x", "z"),
		mkev(15, "a", "x", "z"), mkev(0, "a", "x", "z"),
		mkev(20, "b", "x", "z"),
	}
	if err := e.Append(batch); err != nil {
		t.Fatal(err)
	}
	tab, err := e.Tabulate("r1", "d1", "d2", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if tab.Total != 3 || tab.Cells[0][0].Count != 3 {
		t.Fatalf("half-open [10,20) total=%d cells=%v", tab.Total, tab.Cells)
	}
	tab2, _ := e.Tabulate("r1", "d1", "d2", 0, 10)
	// [0,10) 仅含 ts=0 一个事件，k=3 下被主抑制；用 Primary 断言范围裁剪正确。
	if tab2.Primary != 1 || tab2.Cells[0][0].Count != 0 {
		t.Fatalf("[0,10) should contain exactly 1 suppressed event: %+v", tab2)
	}
	t.Logf("half-open ok rows=%v cols=%v total=%d", tab.Rows, tab.Cols, tab.Total)
}

func TestErrorOrdering(t *testing.T) {
	e := newTestEngine(10000, 100000)
	if _, err := e.Tabulate("ghost", "d1", "d2", 10, 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("want invalid range, got %v", err)
	}
	if _, err := e.Tabulate("ghost", "nope", "d2", 0, 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("want invalid dim, got %v", err)
	}
	if _, err := e.Tabulate("ghost", "d1", "d1", 0, 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("want same-dim invalid, got %v", err)
	}
	if _, err := e.Tabulate("ghost", "d1", "d2", 0, 10); !errors.Is(err, ErrUnknownRole) {
		t.Fatalf("want unknown role, got %v", err)
	}
	if _, err := e.Tabulate("r1", "d1", "d3", 0, 10); !errors.Is(err, ErrForbidden) {
		t.Fatalf("want forbidden, got %v", err)
	}
}

func TestClearanceAndKByLevel(t *testing.T) {
	e := newTestEngine(10000, 100000)
	if _, err := e.Tabulate("r2", "d2", "d3", 0, 10); !errors.Is(err, ErrForbidden) {
		t.Fatalf("level2 must not see level3 dim: %v", err)
	}
	if err := e.Append([]Event{mkev(1, "a", "x", "z"), mkev(2, "a", "x", "z")}); err != nil {
		t.Fatal(err)
	}
	tab, err := e.Tabulate("r3", "d2", "d3", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if tab.Primary != 1 || !tab.Cells[0][0].Suppressed {
		t.Fatalf("max level 3 => k=7 should suppress 2: %+v", tab.Cells[0][0])
	}
	tab1, _ := e.Tabulate("r1", "d1", "d2", 0, 10)
	if tab1.Primary != 1 {
		t.Fatalf("max level 1 => k=3 should suppress 2 too: primary=%d", tab1.Primary)
	}
	t.Logf("sensitive dim raises k: level3=%+v level1=%+v", tab, tab1)
}

func TestKBoundaryEqual(t *testing.T) {
	e := newTestEngine(10000, 100000)
	for i := 0; i < 3; i++ {
		if err := e.Append([]Event{mkev(int64(i), "a", "x", "z")}); err != nil {
			t.Fatal(err)
		}
	}
	tab, _ := e.Tabulate("r1", "d1", "d2", 0, 100)
	if tab.Cells[0][0].Suppressed || tab.Cells[0][0].Count != 3 {
		t.Fatalf("c==k must not be suppressed: %+v", tab.Cells[0][0])
	}
	if err := e.Append([]Event{mkev(100, "b", "y", "z"), mkev(101, "b", "y", "z")}); err != nil {
		t.Fatal(err)
	}
	tab, _ = e.Tabulate("r1", "d1", "d2", 0, 200)
	// a,x = 3（恰等 k，可见）；b,y = 2（k-1，主抑制），不同行故无互补牵连。
	ax := tab.Cells[0][0] // rows=[a,b], cols=[x,y]
	by := tab.Cells[1][1]
	if ax.Suppressed || ax.Count != 3 || !by.Suppressed || by.Count != 0 {
		t.Fatalf("boundary: ax=%+v by=%+v (cells=%v)", ax, by, tab.Cells)
	}
}

func TestInvalidBatchAndCapacity(t *testing.T) {
	e := newTestEngine(10000, 10)
	seed := []Event{
		mkev(1, "a", "x", "z"), mkev(2, "a", "x", "z"),
		mkev(3, "a", "x", "z"), mkev(4, "a", "x", "z"),
		mkev(5, "a", "x", "z"),
	}
	if err := e.Append(seed); err != nil {
		t.Fatal(err)
	}
	bad := []Event{
		mkev(3, "c", "p", "z"),
		{Ts: 4, Dims: map[string]string{"d1": "c", "d2": "p"}},
	}
	if err := e.Append(bad); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("want invalid, got %v", err)
	}
	tab, _ := e.Tabulate("r1", "d1", "d2", 0, 100)
	if tab.Total != 5 {
		t.Fatalf("rejected batch must not change state, total=%d", tab.Total)
	}

	long := make([]byte, 65)
	for i := range long {
		long[i] = 'q'
	}
	cases := [][]Event{
		{{Ts: 1, Dims: map[string]string{"d1": "", "d2": "y", "d3": "z"}}},
		{{Ts: 1, Dims: map[string]string{"d1": string(long), "d2": "y", "d3": "z"}}},
		{{Ts: -1, Dims: map[string]string{"d1": "a", "d2": "y", "d3": "z"}}},
		{{Ts: 1e13 + 1, Dims: map[string]string{"d1": "a", "d2": "y", "d3": "z"}}},
		{{Ts: 1, Dims: map[string]string{"d1": "a", "d2": "y", "d3": "z", "extra": "w"}}},
		{},
	}
	for i, c := range cases {
		if err := e.Append(c); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("case %d want invalid, got %v", i, err)
		}
	}

	over := []Event{
		mkev(6, "a", "x", "z"), mkev(7, "a", "x", "z"),
		mkev(8, "a", "x", "z"), mkev(9, "a", "x", "z"),
		mkev(10, "a", "x", "z"), mkev(11, "a", "x", "z"),
	}
	if err := e.Append(over); !errors.Is(err, ErrCapacity) {
		t.Fatalf("want capacity, got %v", err)
	}
	tab, _ = e.Tabulate("r1", "d1", "d2", 0, 100)
	if tab.Total != 5 {
		t.Fatalf("capacity reject must not mutate, total=%d", tab.Total)
	}
}

func TestSizeLimit(t *testing.T) {
	e0 := newTestEngine(10, 100)
	empty, err := e0.Tabulate("r1", "d1", "d2", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Total != 0 || len(empty.Rows) != 0 || len(empty.Cols) != 0 {
		t.Fatalf("no events in range must be empty table: %+v", empty)
	}

	e := newTestEngine(3, 100000)
	batch := []Event{
		mkev(1, "a", "x", "z"), mkev(2, "a", "y", "z"),
		mkev(3, "b", "x", "z"), mkev(4, "b", "y", "z"),
	}
	if err := e.Append(batch); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Tabulate("r1", "d1", "d2", 0, 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want too large (2x2=4>3), got %v", err)
	}
	tab, err := e.Tabulate("r1", "d1", "d2", 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	// 窄范围下行集 1×列集 2=2<=3，规模门通过；两个格计数各 1 被主抑制。
	if len(tab.Rows) != 1 || len(tab.Cols) != 2 || tab.Primary != 2 {
		t.Fatalf("narrow range must pass size gate: rows=%v cols=%v primary=%d",
			tab.Rows, tab.Cols, tab.Primary)
	}
}
