package reindex_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/reindex"
)

// naiveRec 是朴素模拟器中的一条目标记录。
type naiveRec struct {
	ver      int64
	alive    bool
	incompat bool
	body     string
}

// naiveModel 与实现走完全相同的输入序列，独立计算期望结果。
type naiveModel struct {
	live     map[string]naiveRec
	seq      int64
	snap     []naiveRec
	snapIDs  []string
	scanned  int
	started  bool
	finished bool
	switched bool
	dst      map[string]naiveRec
	conflict int64
	L        int
}

func newNaive(L int) *naiveModel {
	return &naiveModel{live: map[string]naiveRec{}, dst: map[string]naiveRec{}, L: L}
}

func sortedKeys(m map[string]naiveRec) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
	return ids
}

func (m *naiveModel) put(id, body string) int64 {
	m.seq++
	m.live[id] = naiveRec{ver: m.seq, alive: true, body: body}
	if m.started && !m.switched {
		m.index(id, body, m.seq)
	}
	return m.seq
}

func (m *naiveModel) del(id string) (int64, bool) {
	if _, ok := m.live[id]; !ok {
		return 0, false
	}
	m.seq++
	delete(m.live, id)
	if m.started && !m.switched {
		m.destDelete(id, m.seq)
	}
	return m.seq, true
}

func (m *naiveModel) index(id, body string, ver int64) (applied, incompat bool) {
	cur, exists := m.dst[id]
	if exists && ver <= cur.ver {
		m.conflict++
		return false, false
	}
	if len(body) > m.L {
		m.dst[id] = naiveRec{ver: ver, incompat: true}
		return true, true
	}
	m.dst[id] = naiveRec{ver: ver, alive: true, body: body}
	return true, false
}

func (m *naiveModel) destDelete(id string, ver int64) bool {
	cur, exists := m.dst[id]
	if exists && ver <= cur.ver {
		m.conflict++
		return false
	}
	m.dst[id] = naiveRec{ver: ver}
	return true
}

func (m *naiveModel) start(B int) {
	m.started = true
	ids := sortedKeys(m.live)
	m.snap = make([]naiveRec, 0, len(ids))
	m.snapIDs = ids
	for _, id := range ids {
		r := m.live[id]
		m.snap = append(m.snap, naiveRec{ver: r.ver, alive: true, body: r.body})
	}
}

func (m *naiveModel) step(B int) (ap, cf, ic int, done bool, scanned int) {
	if m.scanned >= len(m.snap) {
		m.finished = true
		return 0, 0, 0, true, 0
	}
	start := m.scanned
	end := m.scanned + B
	if end > len(m.snap) {
		end = len(m.snap)
	}
	for i := m.scanned; i < end; i++ {
		id := m.snapIDs[i]
		r := m.snap[i]
		a, inc := m.index(id, r.body, r.ver)
		switch {
		case a && inc:
			ic++
		case a:
			ap++
		default:
			cf++
		}
	}
	m.scanned = end
	m.finished = m.scanned >= len(m.snap)
	return ap, cf, ic, m.finished, end - start
}

func (m *naiveModel) failedSize() int {
	n := 0
	for _, r := range m.dst {
		if r.incompat {
			n++
		}
	}
	return n
}

func (m *naiveModel) cutover(tol int) bool {
	if !m.finished || m.failedSize() > tol {
		return false
	}
	for id, r := range m.dst {
		if !r.alive {
			delete(m.dst, id)
		}
	}
	m.switched = true
	return true
}

// TestRandomInterleave1500：B=1..5，每两次源写入之间插 0..2 次 Step，
// 实现与逐步朴素模拟器逐项对照 1500 组随机序列。
func TestRandomInterleave1500(t *testing.T) {
	const groups = 1500
	bodies := []string{"", "x", "yy", "zzz", "toolong", "verylongbody"}
	idPool := []string{"a", "b", "c", "d", "e"}
	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(int64(g*7919 + 1)))
		L := 1 + rng.Intn(8)
		B := 1 + g%5
		c, err := reindex.New(L)
		if err != nil {
			t.Fatalf("group %d: %v", g, err)
		}
		m := newNaive(L)
		trace := []string{fmt.Sprintf("L=%d B=%d", L, B)}

		stepOnce := func(tag string) {
			remain := len(m.snap) - m.scanned
			ap, cf, ic, done, e := c.Step()
			map_, mcf, mic, mdone, read := m.step(B)
			if e != nil || ap != map_ || cf != mcf || ic != mic || done != mdone {
				t.Fatalf("g%d %s step: impl(%d,%d,%d,%v,%v) model(%d,%d,%d,%v)\n%v",
					g, tag, ap, cf, ic, done, e, map_, mcf, mic, mdone, trace)
			}
			want := B
			if remain < want {
				want = remain
			}
			if read != want || ap+cf+ic != read {
				t.Fatalf("g%d batch size read=%d want=%d sum=%d remain=%d\n%v", g, read, want, ap+cf+ic, remain, trace)
			}
			trace = append(trace, fmt.Sprintf("Step->(%d,%d,%d,%v)", ap, cf, ic, done))
		}
		writePhase := func(n int) {
			for w := 0; w < n; w++ {
				id := idPool[rng.Intn(len(idPool))]
				if rng.Intn(3) == 0 {
					seq, e := c.Delete(id)
					mseq, ok := m.del(id)
					if ok != (e == nil) || (e == nil && seq != mseq) {
						t.Fatalf("g%d Delete(%s): impl(%d,%v) model(%d,%v)\n%v", g, id, seq, e, mseq, ok, trace)
					}
					trace = append(trace, fmt.Sprintf("Delete(%s)->%d/%v", id, seq, e))
				} else {
					body := bodies[rng.Intn(len(bodies))]
					seq, e := c.Put(id, body)
					mseq := m.put(id, body)
					if e != nil || seq != mseq {
						t.Fatalf("g%d Put(%s,%q): impl(%d,%v) model %d\n%v", g, id, body, seq, e, mseq, trace)
					}
					trace = append(trace, fmt.Sprintf("Put(%s,%q)->%d", id, body, seq))
				}
				if c.State() == reindex.Running && w%2 == 1 {
					for s, k := 0, rng.Intn(3); s < k; s++ {
						stepOnce("inter")
					}
				}
			}
		}

		writePhase(4 + rng.Intn(6))
		if e := c.Start(B); e != nil {
			t.Fatalf("g%d Start: %v\n%v", g, e, trace)
		}
		m.start(B)
		trace = append(trace, fmt.Sprintf("Start(%d)", B))
		writePhase(4 + rng.Intn(10))
		for !m.finished {
			stepOnce("drain")
		}
		// 回填完后的幂等 Step：全 0 与真。
		if ap, cf, ic, done, e := c.Step(); e != nil || ap != 0 || cf != 0 || ic != 0 || !done {
			t.Fatalf("g%d idempotent step after finish (%d,%d,%d,%v,%v)\n%v", g, ap, cf, ic, done, e, trace)
		}

		if c.Dest().Conflicts() != m.conflict {
			t.Fatalf("g%d conflicts impl=%d model=%d\n%v", g, c.Dest().Conflicts(), m.conflict, trace)
		}
		fsz := m.failedSize()
		if len(c.Dest().Failed()) != fsz {
			t.Fatalf("g%d failed size impl=%d model=%d\n%v", g, len(c.Dest().Failed()), fsz, trace)
		}
		tol := []int{-1, fsz - 1, fsz, fsz + 1, 1_000_001}[rng.Intn(5)]
		if tol < 0 || tol > 1_000_000 {
			wantErr(t, c.Cutover(tol), reindex.ErrInvalid, fmt.Sprintf("g%d tol=%d", g, tol))
		} else {
			modelFailed := map[string]struct{}{}
			for id, r := range m.dst {
				if r.incompat {
					modelFailed[id] = struct{}{}
				}
			}
			e := c.Cutover(tol)
			ok := m.cutover(tol)
			if (e == nil) != ok {
				t.Fatalf("g%d Cutover(%d) impl=%v model ok=%v\n%v", g, tol, e, ok, trace)
			}
			trace = append(trace, fmt.Sprintf("Cutover(%d)->%v", tol, e))
			if e == nil {
				alive := c.Dest().Records()
				wantSize := 0
				for id := range m.live {
					if _, bad := modelFailed[id]; !bad {
						wantSize++
					}
				}
				if len(alive) != wantSize {
					t.Fatalf("g%d post-cutover size impl=%d want(live-failed)=%d\n%v", g, len(alive), wantSize, trace)
				}
				for id, sr := range m.live {
					if _, bad := modelFailed[id]; bad {
						if _, present := alive[id]; present {
							t.Fatalf("g%d failed id %s must not survive cutover\n%v", g, id, trace)
						}
						continue
					}
					r := alive[id]
					if !r.Alive || r.Ver != sr.ver || r.Body != sr.body {
						t.Fatalf("g%d post-cutover %s impl=%+v source=%+v\n%v", g, id, r, sr, trace)
					}
				}
			}
		}
		t.Logf("g%d: %v => conflicts=%d failed=%d cutover-tol=%d", g, trace, m.conflict, fsz, tol)
	}
}
