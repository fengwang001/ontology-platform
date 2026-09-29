package rebalance

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
)

const keyCount = 60

func testKeys() []string {
	keys := make([]string, keyCount)
	for i := range keys {
		keys[i] = fmt.Sprintf("k-%03d", i)
	}
	return keys
}

func newPopulated(t *testing.T, n int) *Migrator {
	t.Helper()
	m, err := New(n)
	if err != nil {
		t.Fatalf("New(%d): %v", n, err)
	}
	for _, k := range testKeys() {
		if err := m.Put(k, "v-"+k); err != nil {
			t.Fatalf("put %s: %v", k, err)
		}
	}
	return m
}

// logState 打印操作、各分区内容、游标与路由判定依据。
func logState(t *testing.T, op string, m *Migrator) {
	t.Helper()
	d := m.Dump()
	var b strings.Builder
	fmt.Fprintf(&b, "OP=%s n=%d migrating=%v", op, d.PartitionCount, d.Migrating)
	if d.Migrating {
		fmt.Fprintf(&b, " %d->%d cursor=%d", d.From, d.To, d.Cursor)
	}
	fmt.Fprint(&b, "\n  partitions:")
	ps := make([]int, 0, len(d.Partitions))
	for p := range d.Partitions {
		ps = append(ps, p)
	}
	sort.Ints(ps)
	for _, p := range ps {
		ks := make([]string, 0, len(d.Partitions[p]))
		for k := range d.Partitions[p] {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		fmt.Fprintf(&b, "\n    p%d(%d)=%v", p, len(ks), ks)
	}
	if d.Migrating {
		fmt.Fprintf(&b, "\n  basis: idx<cursor(%d) -> hash mod %d, else hash mod %d",
			d.Cursor, d.To, d.From)
	} else {
		fmt.Fprintf(&b, "\n  basis: hash(key) FNV-1a mod %d", d.PartitionCount)
	}
	t.Log(b.String())
}

func assertAllReadable(t *testing.T, m *Migrator) {
	t.Helper()
	for _, k := range testKeys() {
		v, ok, err := m.Get(k)
		if err != nil || !ok || v != "v-"+k {
			t.Fatalf("key %s: ok=%v v=%q err=%v", k, ok, v, err)
		}
	}
}

func assertExactlyOncePlacement(t *testing.T, m *Migrator) {
	t.Helper()
	seen := map[string]int{}
	limit := maxInt(m.fromN, m.toN)
	for p := 0; p < limit; p++ {
		for k := range m.parts[p] {
			if prev, dup := seen[k]; dup {
				t.Fatalf("key %s duplicated in p%d and p%d", k, prev, p)
			}
			seen[k] = p
			want := m.currentOwnerLocked(k)
			if p != want {
				t.Fatalf("key %s in p%d but cursor-routed owner is p%d", k, p, want)
			}
		}
	}
}

func assertOwnership(t *testing.T, m *Migrator, n int) {
	t.Helper()
	for _, k := range testKeys() {
		p := OwnerOf(k, n)
		if v, ok := m.parts[p][k]; !ok || v != "v-"+k {
			t.Fatalf("key %s not at recomputed owner p%d", k, p)
		}
	}
	total := 0
	for p := 0; p < n; p++ {
		total += len(m.parts[p])
	}
	if total != keyCount {
		t.Fatalf("record count %d, want %d", total, keyCount)
	}
}

func TestInvalidPartitionCount(t *testing.T) {
	for _, n := range []int{0, -1, -7} {
		if _, err := New(n); !errors.Is(err, ErrInvalidPartitionCount) {
			t.Fatalf("New(%d) err=%v, want ErrInvalidPartitionCount", n, err)
		}
	}
	m := newPopulated(t, 3)
	before := m.Dump()
	for _, n := range []int{0, -3} {
		if err := m.StartRebalance(n); !errors.Is(err, ErrInvalidPartitionCount) {
			t.Fatalf("StartRebalance(%d) err=%v", n, err)
		}
	}
	if m.Progress().Migrating {
		t.Fatal("failed start must not enter migrating state")
	}
	if fmt.Sprint(m.Dump().Partitions) != fmt.Sprint(before.Partitions) {
		t.Fatal("failed start changed records")
	}
	logState(t, "invalid-target-rejected", m)
}

func TestNonMigratingAndDuplicateAndIncompleteCommit(t *testing.T) {
	m := newPopulated(t, 4)

	if _, err := m.Step(); !errors.Is(err, ErrNotMigrating) {
		t.Fatalf("Step outside migration err=%v", err)
	}
	if err := m.Commit(); !errors.Is(err, ErrNotMigrating) {
		t.Fatalf("Commit outside migration err=%v", err)
	}

	if err := m.StartRebalance(7); err != nil {
		t.Fatal(err)
	}
	if err := m.StartRebalance(9); !errors.Is(err, ErrRebalanceInProgress) {
		t.Fatalf("duplicate rebalance err=%v", err)
	}
	before := m.Dump()
	if err := m.Commit(); !errors.Is(err, ErrMigrationIncomplete) {
		t.Fatalf("early commit err=%v", err)
	}
	if m.Dump().Cursor != before.Cursor || m.PartitionCount() != 4 {
		t.Fatal("failed commit changed cursor or partition count")
	}

	// 迁移一部分后再次提交仍被拒绝，且状态不变。
	moved := 0
	for i := 0; i < 3; i++ {
		ok, err := m.Step()
		if err != nil || !ok {
			t.Fatalf("step: moved=%v err=%v", ok, err)
		}
		moved++
	}
	logState(t, "mid-migration-commit-rejected", m)
	if err := m.Commit(); !errors.Is(err, ErrMigrationIncomplete) {
		t.Fatalf("mid commit err=%v", err)
	}
	if p := m.Progress(); !p.Migrating || p.Cursor != moved {
		t.Fatalf("progress after failed commit: %+v", p)
	}
}

func TestDualRouteReadWrite(t *testing.T) {
	m := newPopulated(t, 3)
	if err := m.StartRebalance(5); err != nil {
		t.Fatal(err)
	}
	pg := m.Progress()
	if pg.From != 3 || pg.To != 5 || pg.Cursor != 0 || pg.Total == 0 {
		t.Fatalf("bad progress: %+v", pg)
	}

	// 全新键在迁移期必须被整体拒绝，记录不变。
	before := m.Dump()
	if err := m.Put("brand-new", "x"); !errors.Is(err, ErrNewKeyDuringMigration) {
		t.Fatalf("new key err=%v", err)
	}
	if m.Dump().Migrating != before.Migrating {
		t.Fatal("failed put changed migration state")
	}

	movedKeys := map[string]bool{}
	for {
		ok, err := m.Step()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		key := m.plan[m.cursor-1]
		movedKeys[key] = true

		// 每个游标位置：已迁移键由新分区服务，未迁移键由旧分区服务。
		if p := m.CurrentOwner(key); p != OwnerOf(key, 5) {
			t.Fatalf("moved key %s routed to p%d, want new owner p%d", key, p, OwnerOf(key, 5))
		}
		if v, ok, _ := m.Get(key); !ok || v != "v-"+key {
			t.Fatalf("moved key %s unreadable: %q %v", key, v, ok)
		}
		for _, k2 := range m.plan[m.cursor:] {
			if p := m.CurrentOwner(k2); p != OwnerOf(k2, 3) {
				t.Fatalf("pending key %s routed to p%d, want old owner p%d", k2, p, OwnerOf(k2, 3))
			}
		}
		// 迁移期对已存在键的写入落在当前所在分区，立即可读。
		newVal := "updated@" + key
		if err := m.Put(key, newVal); err != nil {
			t.Fatalf("update existing key: %v", err)
		}
		if v, _, _ := m.Get(key); v != newVal {
			t.Fatalf("update not visible at routed owner: %q", v)
		}
		assertExactlyOncePlacement(t, m)
	}
	logState(t, "all-stepped-before-commit", m)

	// 未提交前分区数仍是旧值；全部键可读。
	if m.PartitionCount() != 3 {
		t.Fatalf("n before commit = %d", m.PartitionCount())
	}
	for _, k := range testKeys() {
		want := "v-" + k
		if movedKeys[k] {
			want = "updated@" + k
		}
		if v, ok, _ := m.Get(k); !ok || v != want {
			t.Fatalf("pre-commit key %s: ok=%v v=%q want %q", k, ok, v, want)
		}
	}

	if err := m.Commit(); err != nil {
		t.Fatal(err)
	}
	logState(t, "committed-5-partitions", m)
	if m.PartitionCount() != 5 {
		t.Fatalf("n after commit = %d", m.PartitionCount())
	}
	// 重算归属核对：每条记录各居其位，更新值保留。
	for _, k := range testKeys() {
		p := OwnerOf(k, 5)
		want := "v-" + k
		if movedKeys[k] {
			want = "updated@" + k
		}
		if v, ok := m.parts[p][k]; !ok || v != want {
			t.Fatalf("after commit key %s at p%d v=%q ok=%v", k, p, v, ok)
		}
	}

	// 提交后新键恢复允许写入。
	if err := m.Put("brand-new", "x"); err != nil {
		t.Fatalf("post-commit put: %v", err)
	}
}

func TestShrinkRebalance(t *testing.T) {
	m := newPopulated(t, 6)
	if err := m.StartRebalance(2); err != nil {
		t.Fatal(err)
	}
	for {
		ok, err := m.Step()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		assertExactlyOncePlacement(t, m)
		assertAllReadable(t, m)
	}
	if err := m.Commit(); err != nil {
		t.Fatal(err)
	}
	assertOwnership(t, m, 2)
	logState(t, "shrunk-6-to-2", m)
}

func TestPlanDeterministicAndSorted(t *testing.T) {
	a := newPopulated(t, 4)
	b := newPopulated(t, 4)
	if err := a.StartRebalance(9); err != nil {
		t.Fatal(err)
	}
	if err := b.StartRebalance(9); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(a.plan) != fmt.Sprint(b.plan) {
		t.Fatal("plan not reproducible across instances")
	}
	if !sort.StringsAreSorted(a.plan) {
		t.Fatal("plan is not sorted")
	}
	for _, k := range a.plan {
		if OwnerOf(k, 4) == OwnerOf(k, 9) {
			t.Fatalf("key %s in plan but owner unchanged", k)
		}
	}
}

func TestStepAfterComplete(t *testing.T) {
	m := newPopulated(t, 3)
	if err := m.StartRebalance(4); err != nil {
		t.Fatal(err)
	}
	var steps int
	for {
		ok, err := m.Step()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		steps++
	}
	if steps != m.Progress().Total {
		t.Fatalf("steps=%d total=%d", steps, m.Progress().Total)
	}
	c := m.Progress().Cursor
	if ok, err := m.Step(); ok || err != nil {
		t.Fatalf("extra step: moved=%v err=%v", ok, err)
	}
	if m.Progress().Cursor != c {
		t.Fatal("step past end advanced cursor")
	}
	if err := m.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Step(); !errors.Is(err, ErrNotMigrating) {
		t.Fatalf("step after commit err=%v", err)
	}
}
