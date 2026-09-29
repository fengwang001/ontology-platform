package antijoin

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

type snapshot struct {
	left, right map[string]Row
	members     map[string]struct{}
	logLen      int
}

func snapshotOf(v *View) snapshot {
	return snapshot{
		left:    v.LeftRows(),
		right:   v.RightRows(),
		members: v.Result(),
		logLen:  len(v.Changelog()),
	}
}

func assertSnapshotUnchanged(t *testing.T, v *View, before snapshot, label string) {
	t.Helper()
	after := snapshotOf(v)
	if !reflect.DeepEqual(after.left, before.left) ||
		!reflect.DeepEqual(after.right, before.right) ||
		!reflect.DeepEqual(after.members, before.members) ||
		after.logLen != before.logLen {
		t.Fatalf("%s: state changed after rejected input\nbefore=%+v\nafter =%+v", label, before, after)
	}
	if err := v.Check(); err != nil {
		t.Fatalf("%s: Check failed after rejection: %v", label, err)
	}
}

func mustApply(t *testing.T, v *View, c Change, wantOps ...Op) []Output {
	t.Helper()
	outs, err := v.Apply(c)
	if err != nil {
		t.Fatalf("Apply(%s) unexpected error: %v", c, err)
	}
	gotOps := make([]Op, len(outs))
	for i, o := range outs {
		gotOps[i] = o.Op
	}
	if len(wantOps) > 0 && !reflect.DeepEqual(gotOps, wantOps) {
		t.Fatalf("Apply(%s) outputs = %v, want ops %v", c, gotOps, wantOps)
	}
	return outs
}

func expectReject(t *testing.T, v *View, c Change, want error) {
	t.Helper()
	_, err := v.Apply(c)
	if !errors.Is(err, want) {
		t.Fatalf("Apply(%s) error = %v, want %v", c, err, want)
	}
}

func assertMember(t *testing.T, v *View, id string, want bool) {
	t.Helper()
	_, ok := v.Result()[id]
	if ok != want {
		t.Fatalf("member %q = %v, want %v", id, ok, want)
	}
	if err := v.Check(); err != nil {
		t.Fatalf("Check failed: %v", err)
	}
}

// 场景：右侧计数在 0/1/2 之间穿越，只有越过零边界时输出。
func TestCountCrossesZero(t *testing.T) {
	v := New(WithLogger(NewPrintLogger(nil)))

	mustApply(t, v, Change{Side: Left, Op: Insert, ID: "L1", Key: "k"})
	mustApply(t, v, Change{Side: Left, Op: Insert, ID: "L2", Key: "other"})

	// 0->1：所有键为 k 的左行离开，按标识排序。
	outs := mustApply(t, v, Change{Side: Right, Op: Insert, ID: "R1", Key: "k"}, Delete)
	if outs[0].ID != "L1" {
		t.Fatalf("leave output id = %q, want L1", outs[0].ID)
	}
	assertMember(t, v, "L1", false)

	// 1->2：无输出。
	mustApply(t, v, Change{Side: Right, Op: Insert, ID: "R2", Key: "k"})
	// 2->1：无输出。
	mustApply(t, v, Change{Side: Right, Op: Delete, ID: "R2", Key: "k"})
	if got := v.RightCount("k"); got != 1 {
		t.Fatalf("right count = %d, want 1", got)
	}
	assertMember(t, v, "L1", false)

	// 1->0：左行进入。
	mustApply(t, v, Change{Side: Right, Op: Delete, ID: "R1", Key: "k"}, Insert)
	assertMember(t, v, "L1", true)
	assertMember(t, v, "L2", true)
}

// 场景：NULL 键与任何值（含另一个 NULL）都不相等。
func TestNullSemantics(t *testing.T) {
	v := New(WithLogger(NewPrintLogger(nil)))

	// NULL 左行插入即进入结果。
	mustApply(t, v, Change{Side: Left, Op: Insert, ID: "LN", Key: nil}, Insert)
	// NULL 右行不匹配任何左行，无输出，NULL 左行仍在结果中。
	mustApply(t, v, Change{Side: Right, Op: Insert, ID: "RN1", Key: nil})
	mustApply(t, v, Change{Side: Right, Op: Insert, ID: "RN2", Key: nil})
	assertMember(t, v, "LN", true)

	// 非空右行同样不匹配 NULL 左行。
	mustApply(t, v, Change{Side: Right, Op: Insert, ID: "R1", Key: "k"})
	assertMember(t, v, "LN", true)

	// NULL 右行的删除对计数和成员均无影响。
	mustApply(t, v, Change{Side: Right, Op: Delete, ID: "RN1", Key: nil})
	assertMember(t, v, "LN", true)

	// NULL 左行删除时作为成员离开。
	mustApply(t, v, Change{Side: Left, Op: Delete, ID: "LN", Key: nil}, Delete)
	if _, ok := v.Result()["LN"]; ok {
		t.Fatalf("LN should be gone after delete")
	}
}

// 场景：右侧先到，左行到达时已被阻塞；右行删空后左行才进入。
func TestRightArrivesFirst(t *testing.T) {
	v := New(WithLogger(NewPrintLogger(nil)))

	mustApply(t, v, Change{Side: Right, Op: Insert, ID: "R1", Key: "k"})
	mustApply(t, v, Change{Side: Right, Op: Insert, ID: "R2", Key: "k"})

	// 左行到达时计数为 2，不输出进入。
	mustApply(t, v, Change{Side: Left, Op: Insert, ID: "L1", Key: "k"})
	assertMember(t, v, "L1", false)

	// 2->1 无输出。
	mustApply(t, v, Change{Side: Right, Op: Delete, ID: "R2", Key: "k"})
	assertMember(t, v, "L1", false)

	// 1->0 时左行进入。
	mustApply(t, v, Change{Side: Right, Op: Delete, ID: "R1", Key: "k"}, Insert)
	assertMember(t, v, "L1", true)

	// 删除结果中的左行输出离开；之后再来右行无左行可影响。
	mustApply(t, v, Change{Side: Left, Op: Delete, ID: "L1", Key: "k"}, Delete)
	mustApply(t, v, Change{Side: Right, Op: Insert, ID: "R3", Key: "k"})
}

// 场景：同一输入产生多条输出时按标识排序。
func TestOutputsSortedByID(t *testing.T) {
	v := New(WithLogger(NewPrintLogger(nil)))
	for _, id := range []string{"b", "a", "d", "c"} {
		mustApply(t, v, Change{Side: Left, Op: Insert, ID: id, Key: 7})
	}

	outs := mustApply(t, v, Change{Side: Right, Op: Insert, ID: "R1", Key: 7}, Delete, Delete, Delete, Delete)
	got := []string{outs[0].ID, outs[1].ID, outs[2].ID, outs[3].ID}
	want := []string{"a", "b", "c", "d"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("output ids = %v, want sorted %v", got, want)
	}

	outs = mustApply(t, v, Change{Side: Right, Op: Delete, ID: "R1", Key: 7}, Insert, Insert, Insert, Insert)
	got = []string{outs[0].ID, outs[1].ID, outs[2].ID, outs[3].ID}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("output ids = %v, want sorted %v", got, want)
	}
}

// 场景：四类非法输入各自返回互可区分的错误，且拒绝后两侧行、计数、视图与日志均不变。
func TestRejectionsLeaveNoTrace(t *testing.T) {
	v := New(WithMaxRows(2), WithLogger(NewPrintLogger(nil)))
	mustApply(t, v, Change{Side: Left, Op: Insert, ID: "L1", Key: "k"})
	mustApply(t, v, Change{Side: Right, Op: Insert, ID: "R1", Key: "k"})

	// 错误类别互不相同。
	cases := []struct {
		name string
		c    Change
		want error
	}{
		{"empty id on left insert", Change{Side: Left, Op: Insert, ID: "", Key: "x"}, ErrEmptyID},
		{"empty id on right delete", Change{Side: Right, Op: Delete, ID: "", Key: nil}, ErrEmptyID},
		{"duplicate left insert", Change{Side: Left, Op: Insert, ID: "L1", Key: "k2"}, ErrDuplicateInsert},
		{"duplicate right insert", Change{Side: Right, Op: Insert, ID: "R1", Key: nil}, ErrDuplicateInsert},
		{"delete missing left", Change{Side: Left, Op: Delete, ID: "nope", Key: "k"}, ErrDeleteNotExist},
		{"delete missing right", Change{Side: Right, Op: Delete, ID: "nope", Key: nil}, ErrDeleteNotExist},
		{"row limit exceeded", Change{Side: Left, Op: Insert, ID: "L2", Key: nil}, ErrRowLimit},
		{"unknown side", Change{Side: Side(9), Op: Insert, ID: "X", Key: 1}, ErrInvalidInput},
		{"unknown op", Change{Side: Left, Op: Op(9), ID: "X", Key: 1}, ErrInvalidInput},
	}
	seen := map[error]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := snapshotOf(v)
			expectReject(t, v, tc.c, tc.want)
			assertSnapshotUnchanged(t, v, before, tc.name)
			seen[tc.want] = true
		})
	}
	if len(seen) < 4 {
		t.Fatalf("expected at least 4 distinct rejection categories, saw %d", len(seen))
	}

	// 行数边界：删掉一行后，原本超限的插入可以成功。
	mustApply(t, v, Change{Side: Right, Op: Delete, ID: "R1", Key: "k"}, Insert)
	before := snapshotOf(v)
	mustApply(t, v, Change{Side: Left, Op: Insert, ID: "L2", Key: nil}, Insert)
	if before.logLen+1 != len(v.Changelog()) {
		t.Fatalf("accepted insert should append exactly one changelog step")
	}
}

// 场景：被拒绝尝试只出现在外部日志器，不进入已提交变更日志。
func TestRejectedAttemptNotInChangelog(t *testing.T) {
	cl := &CollectLogger{}
	v := New(WithLogger(cl))
	mustApply(t, v, Change{Side: Left, Op: Insert, ID: "L1", Key: 1}, Insert)
	expectReject(t, v, Change{Side: Left, Op: Insert, ID: "L1", Key: 2}, ErrDuplicateInsert)
	expectReject(t, v, Change{Side: Left, Op: Insert, ID: "", Key: 2}, ErrEmptyID)

	committed := v.Changelog()
	if len(committed) != 1 || !committed[0].Accepted {
		t.Fatalf("committed changelog should contain only 1 accepted step, got %+v", committed)
	}
	logged := cl.Snapshot()
	if len(logged) != 3 {
		t.Fatalf("external logger should see all 3 attempts, got %d", len(logged))
	}
}

// 生成一组合法的随机变更序列（保证不会触发重复插入/删除不存在/超限）。
func randomValidChanges(t *testing.T, rng *rand.Rand, n, maxRows int) []Change {
	t.Helper()
	type row struct {
		side Side
		key  any
	}
	live := map[string]row{}
	next := 0
	changes := make([]Change, 0, n)
	for i := 0; i < n; i++ {
		side := Side(rng.Intn(2))
		// 收集该侧存活标识。
		var ids []string
		for id, r := range live {
			if r.side == side {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 && (len(live) >= maxRows || rng.Intn(2) == 0) {
			id := ids[rng.Intn(len(ids))]
			r := live[id]
			changes = append(changes, Change{Side: side, Op: Delete, ID: id, Key: r.key})
			delete(live, id)
			continue
		}
		id := fmt.Sprintf("id-%03d", next)
		next++
		var key any
		switch rng.Intn(4) {
		case 0:
			key = nil
		case 1:
			key = rng.Intn(6)
		case 2:
			key = rng.Intn(3)
		default:
			key = fmt.Sprintf("k%d", rng.Intn(4))
		}
		changes = append(changes, Change{Side: side, Op: Insert, ID: id, Key: key})
		live[id] = row{side: side, key: key}
	}
	return changes
}

// 场景：变更日志任意前缀应用后的视图都等于批量重算结果。
func TestEveryPrefixMatchesRecompute(t *testing.T) {
	rng := rand.New(rand.NewSource(20260929))
	changes := randomValidChanges(t, rng, 300, 40)

	// 先完整提交一遍并持续自检，收集已接受输入。
	src := New(WithMaxRows(40), WithLogger(NewPrintLogger(nil)))
	accepted := make([]Change, 0, len(changes))
	for i, c := range changes {
		outs, err := src.Apply(c)
		if err != nil {
			t.Fatalf("step %d unexpected reject: %v (%s)", i, err, c)
		}
		// 输出必须始终按标识排序。
		ids := make([]string, len(outs))
		for j, o := range outs {
			ids[j] = o.ID
		}
		if !sort.StringsAreSorted(ids) {
			t.Fatalf("step %d outputs not sorted: %v", i, ids)
		}
		accepted = append(accepted, c)
		if err := src.Check(); err != nil {
			t.Fatalf("step %d Check: %v", i, err)
		}
		got := src.Result()
		want := Recompute(src.LeftRows(), src.RightRows())
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("step %d incremental view != recompute", i)
		}
	}

	// 对每个前缀在全新视图上重放，必须与批量重算一致。
	for n := 0; n <= len(accepted); n++ {
		v := New(WithMaxRows(40))
		for _, c := range accepted[:n] {
			if _, err := v.Apply(c); err != nil {
				t.Fatalf("prefix %d replay rejected: %v", n, err)
			}
		}
		if err := v.Check(); err != nil {
			t.Fatalf("prefix %d Check: %v", n, err)
		}
		got := v.Result()
		want := Recompute(v.LeftRows(), v.RightRows())
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("prefix %d view != batch recompute", n)
		}
	}
}

// 场景：多个执行体并发提交、查询与自检，且可互相并发；-race 下验证。
func TestConcurrentCommitsAndChecks(t *testing.T) {
	v := New(WithMaxRows(1<<14), WithLogger(NopLogger{}))
	var wg sync.WaitGroup

	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(worker + 1)))
			for i := 0; i < 200; i++ {
				id := fmt.Sprintf("w%d-%03d", worker, i)
				var key any = rng.Intn(5)
				if rng.Intn(5) == 0 {
					key = nil
				}
				if _, err := v.Apply(Change{Side: Left, Op: Insert, ID: id, Key: key}); err != nil {
					t.Errorf("concurrent insert: %v", err)
					return
				}
				if _, err := v.Apply(Change{Side: Left, Op: Delete, ID: id, Key: key}); err != nil {
					t.Errorf("concurrent delete: %v", err)
					return
				}
			}
		}(w)
	}

	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(100 + worker)))
			for i := 0; i < 200; i++ {
				id := fmt.Sprintf("rw%d-%03d", worker, i)
				key := rng.Intn(5)
				if _, err := v.Apply(Change{Side: Right, Op: Insert, ID: id, Key: key}); err != nil {
					t.Errorf("concurrent right insert: %v", err)
					return
				}
				// 删除语义按标识找到已存储键，事件中的键仅作记录。
				if _, err := v.Apply(Change{Side: Right, Op: Delete, ID: id, Key: key}); err != nil {
					t.Errorf("concurrent right delete: %v", err)
					return
				}
			}
		}(w)
	}

	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				if err := v.Check(); err != nil {
					t.Errorf("concurrent Check: %v", err)
					return
				}
				_ = v.Result()
				_ = v.Changelog()
			}
		}()
	}

	wg.Wait()
	if err := v.Check(); err != nil {
		t.Fatalf("final Check: %v", err)
	}
	if !reflect.DeepEqual(v.Result(), Recompute(v.LeftRows(), v.RightRows())) {
		t.Fatalf("final view != recompute")
	}
}
