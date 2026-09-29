package group

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestGroup(t *testing.T, partitions, maxMembers int) (*Group, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	g, err := New(partitions, maxMembers, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g, &buf
}

func applyOK(t *testing.T, g *Group, changes []Change) Result {
	t.Helper()
	res, err := g.Apply(changes)
	if err != nil {
		t.Fatalf("Apply(%v): unexpected error: %v", changes, err)
	}
	if err := g.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck after %v: %v", changes, err)
	}
	return res
}

func applyReject(t *testing.T, g *Group, changes []Change, want error) {
	t.Helper()
	beforeGen := g.Generation()
	beforeOwners := g.OwnerOf()
	beforeMembers := g.Members()
	_, err := g.Apply(changes)
	if !errors.Is(err, want) {
		t.Fatalf("Apply(%v): want error matching %v, got %v", changes, want, err)
	}
	if g.Generation() != beforeGen {
		t.Fatalf("rejected batch changed generation: before=%d after=%d", beforeGen, g.Generation())
	}
	if owners := g.OwnerOf(); !equalOwners(owners, beforeOwners) {
		t.Fatalf("rejected batch changed owners: before=%v after=%v", beforeOwners, owners)
	}
	if members := g.Members(); !equalIDSlice(members, beforeMembers) {
		t.Fatalf("rejected batch changed members: before=%v after=%v", beforeMembers, members)
	}
	if err := g.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck after rejected batch: %v", err)
	}
}

func equalOwners(a, b []MemberID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalIDSlice(a, b []MemberID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// assertBalanced 校验每个分区唯一有主且任意两成员持有数之差不大于一。
func assertBalanced(t *testing.T, g *Group, partitions int) map[MemberID]int {
	t.Helper()
	owners := g.OwnerOf()
	if len(owners) != partitions {
		t.Fatalf("owners length = %d, want %d", len(owners), partitions)
	}
	counts := map[MemberID]int{}
	for p, owner := range owners {
		if owner == "" {
			t.Fatalf("partition %d unassigned while group non-empty", p)
		}
		counts[owner]++
	}
	assignment := g.Assignment()
	min, max := partitions+1, 0
	for m, parts := range assignment {
		sorted := append([]int32(nil), parts...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		for i := range sorted {
			if parts[i] != sorted[i] {
				t.Fatalf("member %s partitions not sorted: %v", m, parts)
			}
		}
		if counts[m] != len(parts) {
			t.Fatalf("count mismatch for %s: owners=%d held=%d", m, counts[m], len(parts))
		}
		if len(parts) < min {
			min = len(parts)
		}
		if len(parts) > max {
			max = len(parts)
		}
	}
	for m, c := range counts {
		if _, ok := assignment[m]; !ok {
			t.Fatalf("member %s missing from assignment", m)
		}
		if c < min {
			min = c
		}
		if c > max {
			max = c
		}
	}
	if max-min > 1 {
		t.Fatalf("unbalanced counts: min=%d max=%d", min, max)
	}
	return counts
}

// countMigrations 按规则统计 owners 间的迁移数。
func countMigrations(before, after []MemberID) int {
	migrations := 0
	for p := range before {
		if before[p] != "" && before[p] != after[p] {
			migrations++
		}
	}
	return migrations
}

// bruteForceMinMigrations 暴力枚举所有均衡分配，返回相对 before 的最小迁移数。
// members 为批后成员；枚举每位分区归属任一成员，仅保留极差不超过 1 的分配。
func bruteForceMinMigrations(partitions int, before []MemberID, members []MemberID) int {
	if len(members) == 0 {
		migrations := 0
		for _, owner := range before {
			if owner != "" {
				migrations++
			}
		}
		return migrations
	}
	min := partitions + 1
	assign := make([]MemberID, partitions)
	counts := make(map[MemberID]int, len(members))
	for _, m := range members {
		counts[m] = 0
	}
	base, rem := partitions/len(members), partitions%len(members)
	upper := base + 1
	var enumerate func(p int)
	enumerate = func(p int) {
		if p == partitions {
			if rem == 0 {
				for _, m := range members {
					if counts[m] != base {
						return
					}
				}
			} else {
				gotRem := 0
				for _, m := range members {
					if counts[m] == upper {
						gotRem++
					} else if counts[m] != base {
						return
					}
				}
				if gotRem != rem {
					return
				}
			}
			if migrations := countMigrations(before, assign); migrations < min {
				min = migrations
			}
			return
		}
		for _, m := range members {
			if counts[m] >= upper {
				continue
			}
			assign[p] = m
			counts[m]++
			enumerate(p + 1)
			counts[m]--
		}
	}
	enumerate(0)
	if min > partitions {
		panic("brute force found no balanced assignment")
	}
	return min
}

// buildGroup 直接构造给定属主分布的组，便于精确测试判定依据。
func buildGroup(t *testing.T, maxMembers int, owners []MemberID) *Group {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelInfo}))
	g, err := New(len(owners), maxMembers, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for p, owner := range owners {
		if owner == "" {
			continue
		}
		g.members[owner] = struct{}{}
		g.ownerOf[p] = owner
		g.held[owner] = append(g.held[owner], int32(p))
	}
	if err := g.SelfCheck(); err != nil {
		t.Fatalf("built group fails self-check: %v", err)
	}
	return g
}

func joins(ids ...string) []Change {
	out := make([]Change, len(ids))
	for i, id := range ids {
		out[i] = Change{Type: Join, Member: MemberID(id)}
	}
	return out
}

func leaves(ids ...string) []Change {
	out := make([]Change, len(ids))
	for i, id := range ids {
		out[i] = Change{Type: Leave, Member: MemberID(id)}
	}
	return out
}

func TestInitialJoinBalancesAndNoMigration(t *testing.T) {
	g, _ := newTestGroup(t, 5, DefaultMaxMembers)
	res := applyOK(t, g, joins("a", "b"))
	if res.Generation != 1 || res.Migrations != 0 {
		t.Fatalf("generation=%d migrations=%d, want 1/0", res.Generation, res.Migrations)
	}
	counts := assertBalanced(t, g, 5)
	if counts["a"] != 3 || counts["b"] != 2 {
		t.Fatalf("counts=%v, want a=3 b=2（余数名额按标识升序）", counts)
	}
}

func membersAfter(before []MemberID, changes []Change) []MemberID {
	set := map[MemberID]struct{}{}
	for _, m := range before {
		set[m] = struct{}{}
	}
	for _, c := range changes {
		if c.Type == Join {
			set[c.Member] = struct{}{}
		} else {
			delete(set, c.Member)
		}
	}
	out := make([]MemberID, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func TestJoinLeaveInterleavedWithBruteForce(t *testing.T) {
	g, _ := newTestGroup(t, 7, DefaultMaxMembers)
	steps := [][]Change{
		joins("a", "b", "c"),
		leaves("b"),
		joins("d", "e"),
		leaves("a", "c"),
		joins("f"),
		leaves("d"),
		joins("a", "c"),
		leaves("e", "f"),
	}
	for i, changes := range steps {
		before := g.OwnerOf()
		target := membersAfter(g.Members(), changes)
		res := applyOK(t, g, changes)
		assertBalanced(t, g, 7)
		if got, want := res.Migrations, countMigrations(before, g.OwnerOf()); got != want {
			t.Fatalf("step %d: reported migrations=%d actual=%d", i, got, want)
		}
		if want := bruteForceMinMigrations(7, before, target); res.Migrations != want {
			t.Fatalf("step %d: migrations=%d but brute force optimum=%d", i, res.Migrations, want)
		}
	}
}

func TestRemainderOrderByPreBatchHeldThenID(t *testing.T) {
	// a=3、b=2 时加入 c：余数名额给批前持有更多的 a,b，c 拿基础配额1。
	g := buildGroup(t, DefaultMaxMembers, []MemberID{"a", "b", "a", "a", "b"})
	before := g.OwnerOf()
	res := applyOK(t, g, joins("c"))
	counts := assertBalanced(t, g, 5)
	if counts["a"] != 2 || counts["b"] != 2 || counts["c"] != 1 {
		t.Fatalf("counts=%v, want a=2 b=2 c=1", counts)
	}
	if want := bruteForceMinMigrations(5, before, []MemberID{"a", "b", "c"}); res.Migrations != want || res.Migrations != 1 {
		t.Fatalf("migrations=%d optimum=%d", res.Migrations, want)
	}
}

func TestReleaseLargestPartitionsFirst(t *testing.T) {
	// a 独占全部 4 个分区，加入 b,c,d 后配额恰为 1：
	// a 必须按编号从大到小释放，仅保留 0。
	g := buildGroup(t, DefaultMaxMembers, []MemberID{"a", "a", "a", "a"})
	before := g.OwnerOf()
	res := applyOK(t, g, joins("b", "c", "d"))
	held := g.Assignment()
	if len(held["a"]) != 1 || held["a"][0] != 0 {
		t.Fatalf("a kept %v, want [0]（超配额从大到小释放）", held["a"])
	}
	// a 释放 3 个且全部换主，迁移数等于暴力最优。
	if want := bruteForceMinMigrations(4, before, []MemberID{"a", "b", "c", "d"}); res.Migrations != want {
		t.Fatalf("migrations=%d optimum=%d", res.Migrations, want)
	}
}

func TestFillByLargestGapThenSmallestID(t *testing.T) {
	// 4 个无主分区、成员 a,b：缺口并列时编号升序且标识小者先得 → a={0,2}, b={1,3}。
	g := buildGroup(t, DefaultMaxMembers, []MemberID{"", "", "", ""})
	applyOK(t, g, joins("a", "b"))
	held := g.Assignment()
	if got := fmt.Sprint(held["a"]); got != "[0 2]" {
		t.Fatalf("a=%v, want [0 2]", held["a"])
	}
	if got := fmt.Sprint(held["b"]); got != "[1 3]" {
		t.Fatalf("b=%v, want [1 3]", held["b"])
	}
}

func TestDeterminismAndBatchOrderIndependence(t *testing.T) {
	seed := []MemberID{"a", "a", "b", "b", "c"}
	orders := [][]Change{
		{{Join, "d"}, {Leave, "a"}, {Join, "e"}, {Leave, "c"}},
		{{Leave, "c"}, {Join, "e"}, {Leave, "a"}, {Join, "d"}},
		{{Join, "e"}, {Join, "d"}, {Leave, "c"}, {Leave, "a"}},
	}
	var reference []MemberID
	for i, order := range orders {
		g := buildGroup(t, DefaultMaxMembers, append([]MemberID(nil), seed...))
		applyOK(t, g, order)
		owners := g.OwnerOf()
		if reference == nil {
			reference = owners
			continue
		}
		if !equalOwners(reference, owners) {
			t.Fatalf("order %d: owners=%v, want %v（批内顺序不可观察）", i, owners, reference)
		}
	}

	sequence := [][]Change{
		joins("a", "b"),
		joins("c"),
		leaves("a"),
		joins("d", "e"),
		leaves("c", "d"),
	}
	run := func() []MemberID {
		g, _ := newTestGroup(t, 6, DefaultMaxMembers)
		for _, batch := range sequence {
			applyOK(t, g, batch)
		}
		return g.OwnerOf()
	}
	if first, second := run(), run(); !equalOwners(first, second) {
		t.Fatalf("replay not deterministic: %v vs %v", first, second)
	}
}

func TestInvalidBatchesRejectedAtomically(t *testing.T) {
	g, _ := newTestGroup(t, 4, 3)
	applyOK(t, g, joins("a", "b"))

	applyReject(t, g, joins(""), ErrEmptyMemberID)
	applyReject(t, g, leaves(""), ErrEmptyMemberID)
	applyReject(t, g, joins("a"), ErrDuplicateJoin)
	applyReject(t, g, joins("c", "c"), ErrDuplicateJoin)
	applyReject(t, g, leaves("ghost"), ErrMemberNotFound)
	applyReject(t, g, leaves("a", "a"), ErrMemberNotFound)
	applyReject(t, g, []Change{{Join, "c"}, {Leave, "c"}}, ErrConflictingChange)
	applyReject(t, g, joins("c", "d"), ErrTooManyMembers) // 上限3：2+2=4
	applyReject(t, g, []Change{{Type: 99, Member: "a"}}, ErrUnknownChangeType)

	if g.Generation() != 1 {
		t.Fatalf("generation=%d, want 1（被拒不留痕）", g.Generation())
	}
	counts := assertBalanced(t, g, 4)
	if counts["a"] != 2 || counts["b"] != 2 {
		t.Fatalf("counts=%v, want a=2 b=2", counts)
	}

	sentinels := []error{
		ErrEmptyMemberID, ErrDuplicateJoin, ErrMemberNotFound,
		ErrConflictingChange, ErrTooManyMembers, ErrUnknownChangeType,
	}
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if errors.Is(sentinels[i], sentinels[j]) {
				t.Fatalf("error categories %v and %v not distinguishable", sentinels[i], sentinels[j])
			}
		}
	}
}

func TestEmptyGroupEdge(t *testing.T) {
	g, _ := newTestGroup(t, 3, DefaultMaxMembers)
	applyOK(t, g, joins("a"))
	res := applyOK(t, g, leaves("a"))
	if res.Migrations != 3 || g.Generation() != 2 {
		t.Fatalf("last member leave: migrations=%d gen=%d, want 3/2", res.Migrations, g.Generation())
	}
	if len(g.Members()) != 0 {
		t.Fatalf("members=%v, want empty", g.Members())
	}
	for p, owner := range g.OwnerOf() {
		if owner != "" {
			t.Fatalf("partition %d owned by %q, want unassigned", p, owner)
		}
	}
	if err := g.SelfCheck(); err != nil {
		t.Fatalf("self-check empty group: %v", err)
	}
	res = applyOK(t, g, joins("b"))
	if res.Migrations != 0 {
		t.Fatalf("rejoin migrations=%d, want 0（批前无主不计迁移）", res.Migrations)
	}
	assertBalanced(t, g, 3)
}

func TestInvalidBatchOrderIndependenceAndEmptyBatch(t *testing.T) {
	g, _ := newTestGroup(t, 4, DefaultMaxMembers)
	applyOK(t, g, joins("a"))
	// 同批既加入又离开：无论顺序如何都是冲突，且不留痕。
	applyReject(t, g, []Change{{Join, "z"}, {Leave, "z"}}, ErrConflictingChange)
	applyReject(t, g, []Change{{Leave, "z"}, {Join, "z"}}, ErrConflictingChange)

	// 空批合法：触发一次重分配但无成员变化，分配不动、迁移为0、代数+1。
	before := g.OwnerOf()
	res := applyOK(t, g, nil)
	if res.Migrations != 0 || !equalOwners(before, g.OwnerOf()) {
		t.Fatalf("empty batch: migrations=%d owners changed", res.Migrations)
	}
}

func TestRandomScenariosMatchBruteForce(t *testing.T) {
	const partitions, maxMembers = 8, 5
	rng := rand.New(rand.NewSource(20260929))
	for iter := 0; iter < 300; iter++ {
		// 用轮转+洗牌构造极差不超过1的合法起点。
		m := 2 + rng.Intn(3) // 2..4 个起点成员
		poolIDs := []MemberID{"m0", "m1", "m2", "m3"}
		rng.Shuffle(len(poolIDs), func(i, j int) { poolIDs[i], poolIDs[j] = poolIDs[j], poolIDs[i] })
		startIDs := poolIDs[:m]
		owners := make([]MemberID, partitions)
		for p := range owners {
			owners[p] = startIDs[p%m]
		}
		rng.Shuffle(partitions, func(i, j int) { owners[i], owners[j] = owners[j], owners[i] })
		g := buildGroup(t, maxMembers, owners)

		var changes []Change
		joined := map[MemberID]bool{}
		for _, id := range []MemberID{"m0", "m1", "m2", "m3", "n0", "n1"} {
			if rng.Intn(2) == 0 {
				continue
			}
			current := membersAfter(g.Members(), changes)
			present := false
			for _, x := range current {
				if x == id {
					present = true
				}
			}
			switch {
			case present:
				changes = append(changes, Change{Leave, id})
			case len(current) < maxMembers && !joined[id]:
				changes = append(changes, Change{Join, id})
				joined[id] = true
			}
		}
		if len(changes) == 0 || len(membersAfter(g.Members(), changes)) == 0 {
			continue
		}

		before := g.OwnerOf()
		target := membersAfter(g.Members(), changes)
		res, err := g.Apply(changes)
		if err != nil {
			t.Fatalf("iter %d: unexpected reject: %v", iter, err)
		}
		assertBalanced(t, g, partitions)
		if want := bruteForceMinMigrations(partitions, before, target); res.Migrations != want {
			t.Fatalf("iter %d changes=%v: migrations=%d optimum=%d", iter, changes, res.Migrations, want)
		}
		if got := countMigrations(before, g.OwnerOf()); got != res.Migrations {
			t.Fatalf("iter %d: reported=%d actual=%d", iter, res.Migrations, got)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	g, _ := newTestGroup(t, 6, DefaultMaxMembers)
	applyOK(t, g, joins("a", "b", "c"))

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			counter := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				id := MemberID(fmt.Sprintf("x%d", counter%4))
				counter++
				present := false
				for _, m := range g.Members() {
					if m == id {
						present = true
					}
				}
				var c Change
				if present {
					c = Change{Leave, id}
				} else {
					c = Change{Join, id}
				}
				if _, err := g.Apply([]Change{c}); err == nil {
					if err := g.SelfCheck(); err != nil {
						t.Errorf("concurrent SelfCheck: %v", err)
						return
					}
				}
				rng.Intn(1)
			}
		}(int64(w + 1))
	}
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = g.Generation()
				_ = g.OwnerOf()
				_ = g.Assignment()
				_ = g.Members()
				_, _ = g.Owner(0)
				_ = g.Snapshot()
				if err := g.SelfCheck(); err != nil {
					t.Errorf("reader SelfCheck: %v", err)
					return
				}
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
	if err := g.SelfCheck(); err != nil {
		t.Fatalf("final SelfCheck: %v", err)
	}
}

func TestLogsContainInputsDecisionsAndAssignment(t *testing.T) {
	g, buf := newTestGroup(t, 5, DefaultMaxMembers)
	applyOK(t, g, joins("a", "b", "c"))
	logs := buf.String()
	for _, want := range []string{
		"apply batch: received input",
		"rebalance: quota decided",
		"rebalance: fill unassigned partition",
		"rebalance: migration counted",
		"apply batch: committed",
		"quota=",
		"remainder_order=",
		"assignment=",
		"migrations=",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("logs missing %q\nlogs:\n%s", want, logs)
		}
	}
	buf.Reset()
	_, err := g.Apply(joins("a"))
	if !errors.Is(err, ErrDuplicateJoin) {
		t.Fatalf("want ErrDuplicateJoin, got %v", err)
	}
	if !strings.Contains(buf.String(), "apply batch: rejected, state unchanged") {
		t.Fatalf("rejection log missing, got:\n%s", buf.String())
	}
}
