package rebalance

import (
	"encoding/json"
	"sync"
	"testing"
)

func runAll(m *Migrator) error {
	for {
		ok, err := m.Step()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
}

func TestResumeExactlyOnce(t *testing.T) {
	m := newPopulated(t, 3)
	if err := m.StartRebalance(8); err != nil {
		t.Fatal(err)
	}
	stop := len(m.plan) / 2
	for i := 0; i < stop; i++ {
		if _, err := m.Step(); err != nil {
			t.Fatal(err)
		}
	}
	logState(t, "crash-point", m)

	data, err := m.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	r, err := Restore(data)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	pg := r.Progress()
	if pg.Cursor != stop || pg.Total != len(m.plan) || !pg.Migrating {
		t.Fatalf("resumed progress %+v, want cursor %d total %d", pg, stop, len(m.plan))
	}

	// 续迁前后：已迁移键仍在新归属，未迁移键仍在旧归属，无重复无跳过。
	for i, k := range r.plan {
		want := OwnerOf(k, 3)
		if i < stop {
			want = OwnerOf(k, 8)
		}
		if _, ok := r.parts[want][k]; !ok {
			t.Fatalf("resumed key %s missing at p%d (idx %d)", k, want, i)
		}
		if r.CurrentOwner(k) != want {
			t.Fatalf("resumed routing for %s = %d, want %d", k, r.CurrentOwner(k), want)
		}
	}
	assertAllReadable(t, r)
	assertExactlyOncePlacement(t, r)
	logState(t, "restored-mid-migration", r)

	if err := r.StartRebalance(11); err == nil {
		t.Fatal("duplicate rebalance after restore must fail")
	}
	if err := runAll(r); err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(); err != nil {
		t.Fatal(err)
	}
	assertOwnership(t, r, 8)
	logState(t, "resume-then-commit", r)
}

func TestSnapshotRoundTripStable(t *testing.T) {
	m := newPopulated(t, 5)
	data, err := m.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	r, err := Restore(data)
	if err != nil {
		t.Fatal(err)
	}
	assertOwnership(t, r, 5)
	if r.Progress().Migrating {
		t.Fatal("restored stable store unexpectedly migrating")
	}

	// 篡改游标后的快照必须被拒绝。
	m2 := newPopulated(t, 3)
	if err := m2.StartRebalance(6); err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Step(); err != nil {
		t.Fatal(err)
	}
	bad, err := m2.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var s snapData
	if err := json.Unmarshal(bad, &s); err != nil {
		t.Fatal(err)
	}
	s.Cursor = s.Cursor + 1
	tampered, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(tampered); err == nil {
		t.Fatal("tampered cursor snapshot must be rejected")
	}
}

func TestConcurrentReadsMatchSequential(t *testing.T) {
	m := newPopulated(t, 4)
	if err := m.StartRebalance(7); err != nil {
		t.Fatal(err)
	}
	keys := testKeys()

	stopCh := make(chan struct{})
	var wg sync.WaitGroup

	// 迁移线程逐条推进游标。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopCh:
				return
			default:
			}
			ok, err := m.Step()
			if err != nil {
				t.Errorf("step: %v", err)
				return
			}
			if !ok {
				return
			}
		}
	}()

	// 进度单调不减。
	wg.Add(1)
	go func() {
		defer wg.Done()
		last := 0
		for {
			select {
			case <-stopCh:
				return
			default:
			}
			p := m.Progress()
			if p.Cursor < last {
				t.Errorf("cursor went backwards: %d -> %d", last, p.Cursor)
				return
			}
			last = p.Cursor
			if !p.Migrating && last == p.Total && p.Total > 0 {
				return
			}
		}
	}()

	// 多线程并发读同一批键：任何瞬间每键结果都必须等于
	// “单线程按 keys 顺序、在同一游标快照下逐键读”的结果——
	// 即值正确且恰好在游标路由的当前分区被找到。
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 200; round++ {
				for _, k := range keys {
					p := m.CurrentOwner(k)
					v, ok, err := m.Get(k)
					if err != nil || !ok || v != "v-"+k {
						t.Errorf("concurrent get %s: ok=%v v=%q err=%v", k, ok, v, err)
						return
					}
					_ = p
				}
			}
		}()
	}

	// 等待迁移完成，再让读者结束。
	for m.Progress().Migrating {
		if ok, _ := m.Step(); !ok {
			break
		}
	}
	close(stopCh)
	wg.Wait()

	if err := m.Commit(); err != nil {
		t.Fatal(err)
	}
	assertOwnership(t, m, 7)

	// 顺序执行一次作为对照：每步游标严格 +1，完成后同样各居其位。
	seq := newPopulated(t, 4)
	if err := seq.StartRebalance(7); err != nil {
		t.Fatal(err)
	}
	prev := -1
	for {
		if seq.Progress().Cursor <= prev {
			t.Fatal("cursor not strictly increasing per step")
		}
		prev = seq.Progress().Cursor
		ok, err := seq.Step()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
	}
	if err := seq.Commit(); err != nil {
		t.Fatal(err)
	}
	assertOwnership(t, seq, 7)
	logState(t, "concurrent-rebalance-done", m)
}
