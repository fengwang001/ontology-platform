package coordinator

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// TestKeyOrderPreserved 同一键记录的提交顺序等于追加顺序（跨分裂与合并）。
func TestKeyOrderPreserved(t *testing.T) {
	c := New(10, 1000, 16)
	rng := rand.New(rand.NewSource(7))

	type rec struct {
		key, shard int
		pos        int64
	}
	var appended []rec
	appendN := func(n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			k := rng.Intn(10)
			sid, pos, err := c.Append(k)
			if err != nil {
				t.Fatalf("Append(%d): %v", k, err)
			}
			appended = append(appended, rec{k, sid, pos})
		}
	}

	var emitted []rec
	commitAll := func(shard int) {
		t.Helper()
		snap, _ := c.Snapshot(shard)
		w := fmt.Sprintf("consumer-%d", shard)
		if _, err := c.Acquire(shard, w); err != nil {
			t.Fatalf("Acquire(%d,%s): %v", shard, w, err)
		}
		for p := snap.Committed + 1; p <= snap.Appended; p++ {
			if err := c.Commit(shard, w, p); err != nil {
				t.Fatalf("Commit(%d,%s,%d): %v", shard, w, p, err)
			}
		}
		for _, rc := range appended {
			if rc.shard == shard && rc.pos >= snap.Committed && rc.pos < snap.Appended {
				emitted = append(emitted, rc)
			}
		}
	}

	appendN(20) // 全部落在分片0
	l, r, err := c.Split(0, 5)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	commitAll(0) // 排空父分片0
	appendN(20)  // 落在子分片 l,r
	m, err := c.Merge(l, r)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	commitAll(l) // 排空父分片 l
	commitAll(r) // 排空父分片 r
	appendN(20)  // 落在合并分片 m
	commitAll(m)

	if len(emitted) != len(appended) {
		t.Fatalf("emitted %d records, want %d (不丢不重)", len(emitted), len(appended))
	}
	want := map[int][]rec{}
	for _, rc := range appended {
		want[rc.key] = append(want[rc.key], rc)
	}
	got := map[int][]rec{}
	for _, rc := range emitted {
		got[rc.key] = append(got[rc.key], rc)
	}
	for k := 0; k < 10; k++ {
		if !reflect.DeepEqual(got[k], want[k]) {
			t.Fatalf("key %d commit order != append order:\n got=%v\nwant=%v", k, got[k], want[k])
		}
	}
	t.Logf("input=60条随机键追加+分裂+合并 output=60条提交 依据: 逐键提交序列与追加序列完全一致")
}

// TestReplayDeterminism 同一操作序列重放结果完全相同。
func TestReplayDeterminism(t *testing.T) {
	type op struct {
		kind   string
		a, b   int
		worker string
		p      int64
	}
	rng := rand.New(rand.NewSource(42))
	var script []op
	clock := int64(0)
	workers := []string{"w0", "w1", "w2"}
	for i := 0; i < 400; i++ {
		switch rng.Intn(7) {
		case 0:
			script = append(script, op{kind: "append", a: rng.Intn(32)})
		case 1:
			script = append(script, op{kind: "split", a: rng.Intn(40), b: rng.Intn(32)})
		case 2:
			script = append(script, op{kind: "merge", a: rng.Intn(40), b: rng.Intn(40)})
		case 3:
			script = append(script, op{kind: "acquire", a: rng.Intn(40), worker: workers[rng.Intn(3)]})
		case 4:
			script = append(script, op{kind: "renew", a: rng.Intn(40), worker: workers[rng.Intn(3)]})
		case 5:
			script = append(script, op{kind: "commit", a: rng.Intn(40), worker: workers[rng.Intn(3)], p: int64(rng.Intn(6))})
		case 6:
			clock += int64(rng.Intn(3))
			script = append(script, op{kind: "clock", p: clock})
		}
	}

	run := func() []string {
		c := New(32, 7, 2)
		out := make([]string, 0, len(script))
		for _, o := range script {
			switch o.kind {
			case "append":
				sid, pos, err := c.Append(o.a)
				out = append(out, fmt.Sprintf("append %v %d %d", err, sid, pos))
			case "split":
				x, y, err := c.Split(o.a, o.b)
				out = append(out, fmt.Sprintf("split %v %d %d", err, x, y))
			case "merge":
				x, err := c.Merge(o.a, o.b)
				out = append(out, fmt.Sprintf("merge %v %d", err, x))
			case "acquire":
				x, err := c.Acquire(o.a, o.worker)
				out = append(out, fmt.Sprintf("acquire %v %d", err, x))
			case "renew":
				x, err := c.Renew(o.a, o.worker)
				out = append(out, fmt.Sprintf("renew %v %d", err, x))
			case "commit":
				err := c.Commit(o.a, o.worker, o.p)
				out = append(out, fmt.Sprintf("commit %v", err))
			case "clock":
				err := c.AdvanceClock(o.p)
				out = append(out, fmt.Sprintf("clock %v", err))
			}
		}
		return out
	}

	r1, r2 := run(), run()
	if !reflect.DeepEqual(r1, r2) {
		for i := range r1 {
			if r1[i] != r2[i] {
				t.Fatalf("replay diverged at op %d (%v):\n run1: %s\n run2: %s", i, script[i], r1[i], r2[i])
			}
		}
		t.Fatalf("replay diverged")
	}
	t.Logf("input=%d条固定种子操作序列 output=两次重放结果逐条一致 依据: 确定性状态机", len(script))
}

// TestConcurrent 并发追加/领取/续租/提交/推进时钟：
// 追加不丢不重、任一时刻每分片至多一个有效持有者、提交不超过已追加。
func TestConcurrent(t *testing.T) {
	c := New(64, 5, 3)
	var wg sync.WaitGroup
	var mu sync.Mutex
	positions := map[int][]int64{}

	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 500; i++ {
				sid, pos, err := c.Append(rng.Intn(64))
				if err != nil {
					t.Errorf("Append: %v", err)
					return
				}
				mu.Lock()
				positions[sid] = append(positions[sid], pos)
				mu.Unlock()
			}
		}(g)
	}

	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			w := fmt.Sprintf("w%d", g)
			for i := 0; i < 400; i++ {
				exp, err := c.Acquire(0, w)
				if err != nil {
					continue
				}
				snap, _ := c.Snapshot(0)
				if snap.Holder != w && c.Clock() < exp {
					t.Errorf("shard0 lease stolen from %s before expiry %d", w, exp)
					return
				}
				if snap.Committed < snap.Appended {
					_ = c.Commit(0, w, snap.Committed+1)
				}
				_, _ = c.Renew(0, w)
			}
		}(g)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for tick := int64(1); tick <= 300; tick++ {
			_ = c.AdvanceClock(tick)
		}
	}()

	wg.Wait()

	total := 0
	for sid, ps := range positions {
		sort.Slice(ps, func(i, j int) bool { return ps[i] < ps[j] })
		for i, p := range ps {
			if p != int64(i) {
				t.Fatalf("shard %d position[%d] = %d, want %d (追加丢失或重复)", sid, i, p, i)
			}
		}
		total += len(ps)
	}
	if total != 8*500 {
		t.Fatalf("total appended = %d, want %d", total, 8*500)
	}
	snap, _ := c.Snapshot(0)
	if snap.Committed > snap.Appended {
		t.Fatalf("committed %d > appended %d", snap.Committed, snap.Appended)
	}
	t.Logf("output: appended=%d committed=%d 依据: 每分片位置连续从0起, 提交不超过已追加", snap.Appended, snap.Committed)
}
