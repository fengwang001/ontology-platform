package sticky

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func memberNames(k int) []string {
	names := make([]string, k)
	for i := range names {
		names[i] = fmt.Sprintf("m%02d", i)
	}
	return names
}

func equalInts(a, b []int) bool {
	if len(a) == 0 {
		a = []int{}
	}
	if len(b) == 0 {
		b = []int{}
	}
	return reflect.DeepEqual(a, b)
}

func ownersOf(m map[string][]int, n int) assignment {
	out := make(assignment, n)
	for id, ps := range m {
		for _, p := range ps {
			out[p] = id
		}
	}
	return out
}

// assertBalanced 校验：全部已分配、持有者是成员、持有数差 <= 1。
func assertBalanced(t *testing.T, a *Assignor, n int) assignment {
	t.Helper()
	if err := a.Check(); err != nil {
		t.Fatalf("check failed: %v", err)
	}
	snap := a.Assignment()
	got := ownersOf(snap, n)
	if len(a.members) > 0 {
		for p, owner := range got {
			if owner == "" {
				t.Fatalf("partition %d unassigned", p)
			}
		}
	}
	return got
}

func TestInitialJoinTieBreak(t *testing.T) {
	// 3 成员 10 分区：配额 4/3/3；余数名额持有数并列时按标识升序。
	a := NewAssignor(10, 0)
	res, err := a.Apply(context.Background(), []Op{
		{Member: "c", Join: true},
		{Member: "a", Join: true},
		{Member: "b", Join: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("step=initial-join input=[c,a,b] gen=%d migrations=%d assignment=%v",
		res.Generation, res.Migrations, res.Assignment)

	// 全是无主分区：每步缺口并列取标识最小，得到交错序列 a,b,c,a,...
	want := map[string][]int{
		"a": {0, 1, 4, 7},
		"b": {2, 5, 8},
		"c": {3, 6, 9},
	}
	for id, ps := range want {
		if !equalInts(res.Assignment[id], ps) {
			t.Fatalf("member %s: got %v want %v", id, res.Assignment[id], ps)
		}
	}
	if res.Migrations != 0 {
		t.Fatalf("initial migrations = %d, want 0", res.Migrations)
	}
}

func TestJoinLeaveInterleaved(t *testing.T) {
	const n = 4
	a := NewAssignor(n, 0)
	ctx := context.Background()

	steps := []struct {
		name string
		ops  []Op
		want map[string][]int
		mig  int
	}{
		{
			name: "join a,b",
			ops:  []Op{{Member: "b", Join: true}, {Member: "a", Join: true}},
			want: map[string][]int{"a": {0, 2}, "b": {1, 3}},
			mig:  0,
		},
		{
			name: "join c",
			ops:  []Op{{Member: "c", Join: true}},
			// 余数名额按持有数降序、并列标识升序给 a；b 持 2 超配额，
			// 按编号从大到小释放 3；缺口最大者 c 拿到无主分区 3。
			want: map[string][]int{"a": {0, 2}, "b": {1}, "c": {3}},
			mig:  1,
		},
		{
			name: "leave a",
			ops:  []Op{{Member: "a", Join: false}},
			// 0、2 无主升序补齐：0 时缺口相同标识小者 b 先得，2 时 c 缺口更大。
			want: map[string][]int{"b": {0, 1}, "c": {2, 3}},
			mig:  2,
		},
		{
			name: "c leave-then-rejoin plus a joins",
			ops: []Op{
				{Member: "c", Join: false},
				{Member: "c", Join: true},
				{Member: "a", Join: true},
			},
			// c 先离后加净效果不变；a 加入：b/c 各持 2，余数名额给 b，
			// 配额为 a=1,b=2,c=1；c 超出，从大到小释放 3，交给缺口最大的 a。
			want: map[string][]int{"a": {3}, "b": {0, 1}, "c": {2}},
			mig:  1,
		},
	}

	for _, st := range steps {
		res, err := a.Apply(ctx, st.ops)
		if err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		t.Logf("step=%s input=%v gen=%d migrations=%d assignment=%v",
			st.name, st.ops, res.Generation, res.Migrations, res.Assignment)
		if res.Migrations != st.mig {
			t.Fatalf("%s: migrations=%d want %d", st.name, res.Migrations, st.mig)
		}
		for id, ps := range st.want {
			if !equalInts(res.Assignment[id], ps) {
				t.Fatalf("%s: member %s got %v want %v", st.name, id, res.Assignment[id], ps)
			}
		}
		assertBalanced(t, a, n)
	}

	if a.Generation() != 4 {
		t.Fatalf("generation=%d want 4", a.Generation())
	}
}

func TestAllLeaveAndEmptyBatch(t *testing.T) {
	a := NewAssignor(3, 0)
	ctx := context.Background()
	if _, err := a.Apply(ctx, []Op{{Member: "a", Join: true}}); err != nil {
		t.Fatal(err)
	}
	res, err := a.Apply(ctx, []Op{{Member: "a", Join: false}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("step=leave-last input=[-a] gen=%d migrations=%d assignment=%v",
		res.Generation, res.Migrations, res.Assignment)
	if res.Migrations != 0 {
		t.Fatalf("last member leaving: migrations=%d want 0", res.Migrations)
	}
	if err := a.Check(); err != nil {
		t.Fatalf("check after all leave: %v", err)
	}
	if a.Generation() != 2 {
		t.Fatalf("generation=%d want 2", a.Generation())
	}

	before := a.Generation()
	res2, err := a.Apply(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("step=empty-batch input=[] gen=%d assignment=%v decision=no-rebalance",
		res2.Generation, res2.Assignment)
	if res2.Generation != before {
		t.Fatalf("empty batch advanced generation %d -> %d", before, res2.Generation)
	}
}

func TestOrderIndependence(t *testing.T) {
	// 同成员、同批前状态下，打乱批内顺序必须逐分区一致。
	rng := rand.New(rand.NewSource(42))
	const n = 7
	pool := memberNames(5)

	baseOps := []Op{
		{Member: pool[0], Join: true},
		{Member: pool[1], Join: true},
		{Member: pool[2], Join: true},
	}
	nextOps := []Op{
		{Member: pool[0], Join: false},
		{Member: pool[3], Join: true},
		{Member: pool[4], Join: true},
	}

	run := func(perm []int) assignment {
		a := NewAssignor(n, 0)
		if _, err := a.Apply(context.Background(), baseOps); err != nil {
			t.Fatal(err)
		}
		shuffled := make([]Op, len(nextOps))
		for i, p := range perm {
			shuffled[i] = nextOps[p]
		}
		res, err := a.Apply(context.Background(), shuffled)
		if err != nil {
			t.Fatal(err)
		}
		return ownersOf(res.Assignment, n)
	}

	ref := run([]int{0, 1, 2})
	for trial := 0; trial < 20; trial++ {
		perm := rng.Perm(len(nextOps))
		got := run(perm)
		t.Logf("step=order trial=%d perm=%v result=%v", trial, perm, got)
		for p := 0; p < n; p++ {
			if got[p] != ref[p] {
				t.Fatalf("partition %d: %q != %q for perm %v", p, got[p], ref[p], perm)
			}
		}
	}
}

func TestMigrationsMatchBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(8)
		m := 1 + rng.Intn(4)
		all := memberNames(m + 2)

		// 随机生成批前分配：成员为 all 的前 m 个，持有数差 <= 1。
		prevMembers := append([]string{}, all[:m]...)
		prev := make(assignment, n)
		base := n / m
		rem := n % m
		order := rng.Perm(n)
		pos := 0
		for i, id := range prevMembers {
			q := base
			if i < rem {
				q++
			}
			for k := 0; k < q; k++ {
				prev[order[pos]] = id
				pos++
			}
		}

		// 批后成员集合随机增删，至少保留一人。
		nextSet := map[string]struct{}{}
		for _, id := range prevMembers {
			if rng.Intn(2) == 0 {
				nextSet[id] = struct{}{}
			}
		}
		for _, id := range all[m:] {
			if rng.Intn(2) == 0 {
				nextSet[id] = struct{}{}
			}
		}
		if len(nextSet) == 0 {
			nextSet[prevMembers[0]] = struct{}{}
		}
		nextMembers := make([]string, 0, len(nextSet))
		for id := range nextSet {
			nextMembers = append(nextMembers, id)
		}
		sort.Strings(nextMembers)

		got, mig := rebalance(prev, nextSet)
		want := bruteForceMinMigrations(prev, nextMembers)

		gotCounts := make(map[string]int)
		for _, owner := range got {
			gotCounts[owner]++
		}
		min, max := n+1, -1
		for _, id := range nextMembers {
			c := gotCounts[id]
			if c < min {
				min = c
			}
			if c > max {
				max = c
			}
		}
		if max-min > 1 {
			t.Fatalf("trial %d unbalanced: %v", trial, gotCounts)
		}
		if mig != want {
			t.Fatalf("trial %d n=%d prev=%v next=%v: migrations=%d brute=%d",
				trial, n, prev, nextMembers, mig, want)
		}
		if trial < 5 {
			t.Logf("step=brute trial=%d input n=%d prev=%v next=%v migrations=%d assignment=%v decision=min-by-brute-force",
				trial, n, prev, nextMembers, mig, got)
		}
	}
}

func TestRejectionsLeaveNoTrace(t *testing.T) {
	const n = 5
	a := NewAssignor(n, 3)
	ctx := context.Background()

	if _, err := a.Apply(ctx, []Op{
		{Member: "a", Join: true},
		{Member: "b", Join: true},
	}); err != nil {
		t.Fatal(err)
	}
	beforeGen := a.Generation()
	beforeSnap := a.Assignment()

	cases := []struct {
		name   string
		ops    []Op
		reason error
	}{
		{"empty-id", []Op{{Member: "", Join: true}}, ErrEmptyMemberID},
		{"duplicate-join-in-batch", []Op{{Member: "c", Join: true}, {Member: "c", Join: true}}, ErrDuplicateJoin},
		{"join-existing", []Op{{Member: "a", Join: true}}, ErrDuplicateJoin},
		{"leave-unknown", []Op{{Member: "ghost", Join: false}}, ErrLeaveUnknown},
		{"member-limit", []Op{{Member: "c", Join: true}, {Member: "d", Join: true}}, ErrMemberLimit},
	}

	for _, tc := range cases {
		_, err := a.Apply(ctx, tc.ops)
		if err == nil {
			t.Fatalf("%s: expected rejection", tc.name)
		}
		var be *BatchError
		if !errors.As(err, &be) {
			t.Fatalf("%s: error %v is not *BatchError", tc.name, err)
		}
		if !errors.Is(err, tc.reason) {
			t.Fatalf("%s: reason=%v want %v", tc.name, err, tc.reason)
		}
		t.Logf("step=%s input=%v decision=rejected reason=%v index=%d member=%q",
			tc.name, tc.ops, be.Reason, be.Index, be.Member)

		if a.Generation() != beforeGen {
			t.Fatalf("%s: generation changed %d -> %d", tc.name, beforeGen, a.Generation())
		}
		if !reflect.DeepEqual(a.Assignment(), beforeSnap) {
			t.Fatalf("%s: assignment changed after rejection", tc.name)
		}
	}

	// 两个“重复加入”子情形共享同一类别；四个错误类别必须两两可区分。
	wantReasons := map[error]bool{
		ErrEmptyMemberID: false,
		ErrDuplicateJoin: false,
		ErrLeaveUnknown:  false,
		ErrMemberLimit:   false,
	}
	for _, tc := range cases {
		for reason := range wantReasons {
			if errors.Is(tc.reason, reason) {
				wantReasons[reason] = true
			}
		}
	}
	for reason, seen := range wantReasons {
		if !seen {
			t.Fatalf("error reason never produced: %v", reason)
		}
	}
}

func TestConcurrent(t *testing.T) {
	const n = 8
	a := NewAssignor(n, 12)
	ctx := context.Background()

	initial := []Op{}
	for _, id := range memberNames(3) {
		initial = append(initial, Op{Member: id, Join: true})
	}
	if _, err := a.Apply(ctx, initial); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(100 + w)))
			for k := 0; k < 200; k++ {
				id := memberNames(12)[rng.Intn(12)]
				op := []Op{{Member: id, Join: rng.Intn(2) == 0}}
				if _, err := a.Apply(ctx, op); err != nil {
					// 非法结果（重复加入/离开不存在/超限）允许，但不得留痕。
					var be *BatchError
					if !errors.As(err, &be) {
						t.Errorf("unexpected error type: %v", err)
						return
					}
				}
				_ = a.Assignment()
				_ = a.Generation()
				if err := a.Check(); err != nil {
					t.Errorf("concurrent check: %v", err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()

	t.Logf("step=concurrent-end gen=%d members=%d assignment=%v",
		a.Generation(), len(a.members), a.Assignment())
	assertBalanced(t, a, n)
}
