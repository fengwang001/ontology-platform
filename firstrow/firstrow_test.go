package firstrow

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func ins(key, id string, t int64) Change {
	return Change{Op: Insert, Row: Row{Key: key, ID: id, Time: t}}
}
func ret(key, id string, t int64) Change {
	return Change{Op: Retract, Row: Row{Key: key, ID: id, Time: t}}
}

func expectEntries(t *testing.T, got []Entry, want []Entry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("entries len = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func expectReject(t *testing.T, err error, idx int, reason Reason) {
	t.Helper()
	reject, ok := AsRejectError(err)
	if !ok {
		t.Fatalf("error %v is not *RejectError", err)
	}
	if reject.Index != idx || reject.Reason != reason {
		t.Fatalf("reject = index %d reason %q, want index %d reason %q",
			reject.Index, reject.Reason, idx, reason)
	}
}

// 排序键并列（时间相同）时按标识字典序打破平局。
func TestTieBreakerByID(t *testing.T) {
	d := New(Options{MaxLiveRows: 10})

	out, err := d.Apply([]Change{ins("k", "b", 5)})
	if err != nil {
		t.Fatal(err)
	}
	expectEntries(t, out, []Entry{{Op: Insert, Row: Row{Key: "k", ID: "b", Time: 5}}})

	out, err = d.Apply([]Change{ins("k", "a", 5)})
	if err != nil {
		t.Fatal(err)
	}
	expectEntries(t, out, []Entry{
		{Op: Retract, Row: Row{Key: "k", ID: "b", Time: 5}},
		{Op: Insert, Row: Row{Key: "k", ID: "a", Time: 5}},
	})

	out, err = d.Apply([]Change{ins("k", "c", 5)})
	if err != nil {
		t.Fatal(err)
	}
	expectEntries(t, out, nil)

	if head := d.Snapshot()["k"]; head.ID != "a" {
		t.Fatalf("head = %q, want a", head.ID)
	}
}

// 可负时间字段：负值更小，应先于正值成为首条。
func TestNegativeTime(t *testing.T) {
	d := New(Options{MaxLiveRows: 10})
	out, err := d.Apply([]Change{ins("k", "later", 100), ins("k", "early", -3)})
	if err != nil {
		t.Fatal(err)
	}
	expectEntries(t, out, []Entry{
		{Op: Insert, Row: Row{Key: "k", ID: "later", Time: 100}},
		{Op: Retract, Row: Row{Key: "k", ID: "later", Time: 100}},
		{Op: Insert, Row: Row{Key: "k", ID: "early", Time: -3}},
	})
	if head := d.Snapshot()["k"]; head.ID != "early" || head.Time != -3 {
		t.Fatalf("head = %+v, want early@-3", head)
	}
}

// 撤回首条后由次早行顶上：先撤回旧首条，再写入新首条。
func TestRetractHeadPromotesNext(t *testing.T) {
	d := New(Options{MaxLiveRows: 10})
	if _, err := d.Apply([]Change{
		ins("k", "a", 1),
		ins("k", "b", 2),
		ins("k", "c", 3),
	}); err != nil {
		t.Fatal(err)
	}

	out, err := d.Apply([]Change{ret("k", "c", 3)})
	if err != nil {
		t.Fatal(err)
	}
	expectEntries(t, out, nil)

	out, err = d.Apply([]Change{ret("k", "a", 1)})
	if err != nil {
		t.Fatal(err)
	}
	expectEntries(t, out, []Entry{
		{Op: Retract, Row: Row{Key: "k", ID: "a", Time: 1}},
		{Op: Insert, Row: Row{Key: "k", ID: "b", Time: 2}},
	})
	if head := d.Snapshot()["k"]; head.ID != "b" {
		t.Fatalf("head = %q, want b", head.ID)
	}

	out, err = d.Apply([]Change{ret("k", "b", 2)})
	if err != nil {
		t.Fatal(err)
	}
	expectEntries(t, out, []Entry{{Op: Retract, Row: Row{Key: "k", ID: "b", Time: 2}}})
	if _, ok := d.Snapshot()["k"]; ok {
		t.Fatal("key k should disappear after its last live row is retracted")
	}
}

// 同一批内首条多次切换，输出顺序必须是每次先撤旧再写新。
func TestBatchHeadChurn(t *testing.T) {
	d := New(Options{MaxLiveRows: 10})
	out, err := d.Apply([]Change{
		ins("k", "c", 3),
		ins("k", "b", 2),
		ins("k", "a", 1),
		ret("k", "a", 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	expectEntries(t, out, []Entry{
		{Op: Insert, Row: Row{Key: "k", ID: "c", Time: 3}},
		{Op: Retract, Row: Row{Key: "k", ID: "c", Time: 3}},
		{Op: Insert, Row: Row{Key: "k", ID: "b", Time: 2}},
		{Op: Retract, Row: Row{Key: "k", ID: "b", Time: 2}},
		{Op: Insert, Row: Row{Key: "k", ID: "a", Time: 1}},
		{Op: Retract, Row: Row{Key: "k", ID: "a", Time: 1}},
		{Op: Insert, Row: Row{Key: "k", ID: "b", Time: 2}},
	})
	if d.LiveRows() != 2 {
		t.Fatalf("live rows = %d, want 2", d.LiveRows())
	}
}

// 各类非法输入：逐类验证可区分原因，并确认整批不产生副作用。
func TestInvalidBatchesRejectedAtomically(t *testing.T) {
	seed := []Change{ins("k", "a", 1)}
	cases := []struct {
		name   string
		batch  []Change
		idx    int
		reason Reason
	}{
		{"empty id", []Change{ins("k", "", 1)}, 0, ReasonEmptyID},
		{"empty key", []Change{ins("", "x", 1)}, 0, ReasonEmptyKey},
		{"empty key checked before empty id", []Change{ins("", "", 1)}, 0, ReasonEmptyKey},
		{"duplicate live id", []Change{ins("k", "a", 9)}, 0, ReasonDuplicateID},
		{"retract unknown id", []Change{ret("k", "ghost", 9)}, 0, ReasonUnknownID},
		{
			"later change invalid rolls back whole batch",
			[]Change{ins("k", "b", 2), ins("k", "a", 1)},
			1,
			ReasonDuplicateID,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := New(Options{MaxLiveRows: 10})
			if _, err := d.Apply(seed); err != nil {
				t.Fatal(err)
			}

			before := d.Snapshot()
			out, err := d.Apply(tc.batch)
			if err == nil {
				t.Fatal("expected rejection error")
			}
			expectReject(t, err, tc.idx, tc.reason)
			expectEntries(t, out, nil)

			after := d.Snapshot()
			if len(after) != len(before) || after["k"] != before["k"] {
				t.Fatalf("state changed by rejected batch: before %+v after %+v", before, after)
			}
			if d.LiveRows() != 1 {
				t.Fatalf("live rows = %d, want 1", d.LiveRows())
			}
		})
	}
}

// 存活行数上限：超限变更被拒绝且整批回滚；不同键合计计数。
func TestLiveRowLimit(t *testing.T) {
	d := New(Options{MaxLiveRows: 2})

	out, err := d.Apply([]Change{ins("k1", "a", 1), ins("k2", "a", 1)})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("entries = %d, want 2", len(out))
	}

	before := d.Snapshot()
	_, err = d.Apply([]Change{ins("k3", "a", 1)})
	if err == nil {
		t.Fatal("expected limit rejection")
	}
	expectReject(t, err, 0, ReasonLimitExceeded)

	after := d.Snapshot()
	if len(after) != len(before) {
		t.Fatalf("state changed after limit rejection: %+v vs %+v", after, before)
	}

	if _, err = d.Apply([]Change{ret("k1", "a", 1)}); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Apply([]Change{ins("k3", "a", 1)}); err != nil {
		t.Fatalf("insert after free slot should succeed: %v", err)
	}
}

// 不同键相互独立；相同标识在不同键下合法。
func TestKeysAreIndependent(t *testing.T) {
	d := New(Options{MaxLiveRows: 10})
	out, err := d.Apply([]Change{ins("k1", "x", 9), ins("k2", "x", 1)})
	if err != nil {
		t.Fatal(err)
	}
	expectEntries(t, out, []Entry{
		{Op: Insert, Row: Row{Key: "k1", ID: "x", Time: 9}},
		{Op: Insert, Row: Row{Key: "k2", ID: "x", Time: 1}},
	})
	snap := d.Snapshot()
	if snap["k1"].ID != "x" || snap["k2"].ID != "x" {
		t.Fatalf("snapshot = %+v", snap)
	}
}

// 同一输入序列反复计算得到完全相同的输出（确定性）。
func TestDeterministicReplay(t *testing.T) {
	script := [][]Change{
		{ins("k", "c", 3), ins("k", "a", 1), ins("k", "b", 2)},
		{ret("k", "a", 1)},
		{ins("k", "d", 0)},
		{ret("k", "d", 0)},
	}

	run := func() ([]Entry, map[string]Row) {
		d := New(Options{MaxLiveRows: 50})
		var all []Entry
		for _, batch := range script {
			out, err := d.Apply(batch)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, out...)
		}
		return all, d.Snapshot()
	}

	first, snap1 := run()
	for attempt := 0; attempt < 5; attempt++ {
		got, snap := run()
		if len(got) != len(first) {
			t.Fatalf("attempt %d: %d entries, want %d", attempt, len(got), len(first))
		}
		for i := range first {
			if got[i] != first[i] {
				t.Fatalf("attempt %d: entry %d = %+v, want %+v", attempt, i, got[i], first[i])
			}
		}
		for key, want := range snap1 {
			if snap[key] != want {
				t.Fatalf("attempt %d: snapshot[%s] = %+v, want %+v", attempt, key, snap[key], want)
			}
		}
	}
}

// 并发读写：-race 下快照逐键一致，存活计数不越界。
func TestConcurrentSnapshotConsistency(t *testing.T) {
	d := New(Options{MaxLiveRows: 1000})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			id := string(rune('a' + i%20))
			_, _ = d.Apply([]Change{{Op: Insert, Row: Row{Key: "k", ID: id, Time: int64(i)}}})
			_, _ = d.Apply([]Change{{Op: Retract, Row: Row{Key: "k", ID: id, Time: int64(i)}}})
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			snap := d.Snapshot()
			if head, ok := snap["k"]; ok {
				if head.Key != "k" || head.ID == "" {
					t.Errorf("inconsistent head in snapshot: %+v", head)
					return
				}
			}
			if live := d.LiveRows(); live < 0 || live > 1000 {
				t.Errorf("live rows out of range: %d", live)
				return
			}
		}
	}()

	wg.Wait()
}

// 日志包含输入、输出条目与判定依据；相同序列重复运行字节一致。
func TestLoggingContentsAndStability(t *testing.T) {
	script := [][]Change{
		{ins("k", "b", 5), ins("k", "a", 5), ins("k", "z", 9)},
		{ret("k", "ghost", 5)},
	}
	run := func() string {
		var buf bytes.Buffer
		d := New(Options{MaxLiveRows: 10, Logger: NewTextLogger(&buf)})
		for _, batch := range script {
			_, _ = d.Apply(batch)
		}
		return buf.String()
	}

	log := run()
	for _, want := range []string{
		`msg="input change"`,
		`msg="output entry"`,
		`msg=decision`,
		`because="smaller sort key becomes head"`,
		`because="retract previous head before new head"`,
		`msg="change rejected; batch dropped, live rows and emitted log untouched"`,
		`reason="retract of non-live id"`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q\n--- log ---\n%s", want, log)
		}
	}
	if again := run(); again != log {
		t.Errorf("log output is not reproducible:\nfirst:\n%s\nagain:\n%s", log, again)
	}
}
