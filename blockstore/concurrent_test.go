package blockstore

import (
	"fmt"
	"sort"
	"sync"
	"testing"
)

// 任意交错下：已提交清单引用的每个块始终存在且可读；状态只可能是 normal/pending/deleted。
func TestConcurrentInterleavingInvariant(t *testing.T) {
	st := New(0)

	var committedMu sync.Mutex
	committed := make(map[string]Digest)

	var producers sync.WaitGroup
	const writers = 8
	const rounds = 5

	for i := 0; i < writers; i++ {
		producers.Add(1)
		go func(i int) {
			defer producers.Done()
			for r := 0; r < rounds; r++ {
				id := fmt.Sprintf("w-%d-%d", i, r)
				bd := Digest(fmt.Sprintf("b-%d-%d", i, r))
				st.BeginSession(id)
				if err := st.Upload(id, bd, []byte("payload")); err != nil {
					t.Errorf("upload: %v", err)
					return
				}
				if err := st.Commit(id, "m-"+string(bd), []Digest{bd}); err != nil {
					t.Errorf("commit: %v", err)
					return
				}
				committedMu.Lock()
				committed["m-"+string(bd)] = bd
				committedMu.Unlock()
				if err := st.EndSession(id); err != nil {
					t.Errorf("end: %v", err)
					return
				}
			}
		}(i)
	}

	// 长会话横跨多个回收轮次：第一轮上传、延迟很久才提交。
	producers.Add(1)
	go func() {
		defer producers.Done()
		id := "long-holder"
		st.BeginSession(id)
		bd := Digest("b-long")
		if err := st.Upload(id, bd, []byte("x")); err != nil {
			t.Errorf("long upload: %v", err)
			return
		}
		// 先等一半写者完成若干提交，再引用待删块提交。
		for {
			committedMu.Lock()
			n := len(committed)
			committedMu.Unlock()
			if n >= writers*rounds/2 {
				break
			}
		}
		if err := st.Commit(id, "m-long", []Digest{bd}); err != nil {
			t.Errorf("long commit: %v", err)
			return
		}
		committedMu.Lock()
		committed["m-long"] = bd
		committedMu.Unlock()
		if err := st.EndSession(id); err != nil {
			t.Errorf("long end: %v", err)
		}
	}()

	stop := make(chan struct{})
	var checkers sync.WaitGroup

	checkers.Add(1)
	go func() {
		defer checkers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := st.GCRound1(); err != nil {
				continue
			}
			for {
				r2, err := st.GCRound2()
				if err != nil || !r2.Skipped {
					break
				}
			}
		}
	}()

	checkers.Add(1)
	go func() {
		defer checkers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			committedMu.Lock()
			mids := make([]string, 0, len(committed))
			for mid := range committed {
				mids = append(mids, mid)
			}
			committedMu.Unlock()
			for _, mid := range mids {
				m, err := st.ReadManifest(mid)
				if err != nil {
					t.Errorf("committed manifest %s unreadable: %v", mid, err)
					return
				}
				for _, bd := range m.Blocks {
					switch state := st.StateOf(bd); state {
					case StateNormal, StatePending:
					default:
						t.Errorf("committed block %s in invalid state %s", bd, state)
						return
					}
					if _, err := st.Read(bd); err != nil {
						t.Errorf("committed block %s missing: %v", bd, err)
						return
					}
				}
			}
		}
	}()

	producers.Wait()
	close(stop)
	checkers.Wait()

	// 收敛：跑完一个完整回收周期后，所有已提交清单依然完整可读。
	if _, err := st.GCRound1(); err != nil {
		t.Fatalf("final round1: %v", err)
	}
	if _, err := st.GCRound2(); err != nil {
		t.Fatalf("final round2: %v", err)
	}
	for mid, bd := range committed {
		m, err := st.ReadManifest(mid)
		if err != nil {
			t.Fatalf("final manifest %s: %v", mid, err)
		}
		if len(m.Blocks) != 1 || m.Blocks[0] != bd {
			t.Fatalf("final manifest %s mismatch: %+v", mid, m)
		}
		if _, err := st.Read(bd); err != nil {
			t.Fatalf("final block %s: %v", bd, err)
		}
	}
}

// 同一操作序列、同一逻辑顺序得到相同的删除集合（结果按摘要排序）。
func TestDeterministicDeletionSet(t *testing.T) {
	run := func() []Digest {
		st := New(0)
		st.BeginSession("s1")
		must(t, st.Upload("s1", dstr(3), []byte("c")))
		must(t, st.Upload("s1", dstr(1), []byte("a")))
		must(t, st.Upload("s1", dstr(2), []byte("b")))
		must(t, st.Commit("s1", "m", []Digest{dstr(1)}))
		must(t, st.EndSession("s1"))
		_, err := st.GCRound1()
		must(t, err)
		r2, err := st.GCRound2()
		must(t, err)
		out := append([]Digest(nil), r2.Deleted...)
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
	first := run()
	for i := 0; i < 5; i++ {
		got := run()
		if len(got) != len(first) {
			t.Fatalf("run %d: %v vs %v", i, got, first)
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("run %d nondeterministic: %v vs %v", i, got, first)
			}
		}
	}
	want := []Digest{dstr(2), dstr(3)}
	if len(first) != len(want) {
		t.Fatalf("deleted=%v want=%v", first, want)
	}
	for i := range want {
		if first[i] != want[i] {
			t.Fatalf("deleted=%v want=%v", first, want)
		}
	}
}
