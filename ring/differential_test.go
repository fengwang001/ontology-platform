package ring

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/change"
)

// naiveSim 是按规则逐步写成的朴素模拟：用无界已见集合去重、
// 用全量重算（取三元组最大者）做仲裁，作为 ring 实现的对拍参照。
type naiveSim struct {
	n        int
	writable map[int]bool
	seen     []map[int]map[int64]bool // 每站点：来源 -> 已见序号集合
	changes  [][]change.Change        // 每站点：全部非 Dup 变更（含本地写）
	clock    []int64
	q        []int64
	queues   map[[2]int][]change.Change
	up       map[[2]int]bool
}

func newNaive(n int, writable []int) *naiveSim {
	m := &naiveSim{
		n:        n,
		writable: make(map[int]bool),
		seen:     make([]map[int]map[int64]bool, n+1),
		changes:  make([][]change.Change, n+1),
		clock:    make([]int64, n+1),
		q:        make([]int64, n+1),
		queues:   make(map[[2]int][]change.Change),
		up:       make(map[[2]int]bool),
	}
	for _, s := range writable {
		m.writable[s] = true
	}
	for i := 1; i <= n; i++ {
		m.seen[i] = make(map[int]map[int64]bool)
		a, b := neighborsOf(n, i)
		m.up[[2]int{i, a}] = true
		m.up[[2]int{i, b}] = true
	}
	return m
}

// winner 返回站点 site 上键 key 的当前获胜记录（全量重算）。
func (m *naiveSim) winner(site int, key string) (Record, bool) {
	var best Record
	found := false
	for _, c := range m.changes[site] {
		if c.Key != key {
			continue
		}
		if !found || best.Triple.Less(c.Triple()) {
			best = Record{Op: c.Op, Val: c.Val, Triple: c.Triple()}
			found = true
		}
	}
	return best, found
}

func (m *naiveSim) write(site int, key string, op change.Op, val int64, now int64) (Result, error) {
	if site < 1 || site > m.n || !change.ValidKey(key) || !op.Valid() || !change.ValidNow(now) {
		return Lost, ErrInvalidParam
	}
	if !m.writable[site] {
		return Lost, ErrNotWritable
	}
	if now < m.clock[site] {
		return Lost, ErrClockRegression
	}
	m.q[site]++
	c := change.Change{Origin: site, Seq: m.q[site], Ts: now, Key: key, Op: op, Val: val}
	m.clock[site] = now
	if m.seen[site][site] == nil {
		m.seen[site][site] = make(map[int64]bool)
	}
	m.seen[site][site][c.Seq] = true
	_, had := m.winner(site, key)
	w, _ := m.winner(site, key)
	res := Applied
	if had && !w.Triple.Less(c.Triple()) {
		res = Lost
	}
	m.changes[site] = append(m.changes[site], c)
	a, b := neighborsOf(m.n, site)
	m.queues[[2]int{site, a}] = append(m.queues[[2]int{site, a}], c)
	m.queues[[2]int{site, b}] = append(m.queues[[2]int{site, b}], c)
	return res, nil
}

func (m *naiveSim) deliver(from, to int) (Result, error) {
	if from < 1 || from > m.n || to < 1 || to > m.n {
		return Dup, ErrInvalidParam
	}
	a, b := neighborsOf(m.n, from)
	if to != a && to != b {
		return Dup, ErrInvalidParam
	}
	link := [2]int{from, to}
	if !m.up[link] {
		return Dup, ErrDown
	}
	if len(m.queues[link]) == 0 {
		return Dup, ErrEmpty
	}
	c := m.queues[link][0]
	m.queues[link] = m.queues[link][1:]
	if m.seen[to][c.Origin][c.Seq] {
		return Dup, nil
	}
	if m.seen[to][c.Origin] == nil {
		m.seen[to][c.Origin] = make(map[int64]bool)
	}
	m.seen[to][c.Origin][c.Seq] = true
	w, had := m.winner(to, c.Key)
	res := Applied
	if had && !w.Triple.Less(c.Triple()) {
		res = Lost
	}
	m.changes[to] = append(m.changes[to], c)
	na, nb := neighborsOf(m.n, to)
	other := na
	if other == from {
		other = nb
	}
	m.queues[[2]int{to, other}] = append(m.queues[[2]int{to, other}], c)
	return res, nil
}

func (m *naiveSim) setLink(from, to int, up bool) error {
	if from < 1 || from > m.n || to < 1 || to > m.n {
		return ErrInvalidParam
	}
	a, b := neighborsOf(m.n, from)
	if to != a && to != b {
		return ErrInvalidParam
	}
	m.up[[2]int{from, to}] = up
	return nil
}

// floorPrefix 计算已见集合意义下的下限：最大的 k 使 1..k 均已见。
func (m *naiveSim) floorPrefix(site, origin int) int64 {
	k := int64(0)
	for m.seen[site][origin][k+1] {
		k++
	}
	return k
}

// sameErr 比较两个错误是否等价（同为 nil 或 errors.Is 匹配）。
func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b)
}

// TestDifferential 用 2000 组随机操作序列对拍 ring 实现与朴素模拟，
// 并在排空后校验全部收敛不变量。日志打印输入、输出与判定依据。
func TestDifferential(t *testing.T) {
	const iterations = 2000
	validKeys := []string{"a", "b", "k", "key-1"}
	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewSource(int64(iter)))
		n := 3 + rng.Intn(6)
		var w []int
		for site := 1; site <= n; site++ {
			if rng.Intn(2) == 0 {
				w = append(w, site)
			}
		}
		if len(w) == 0 {
			w = []int{1 + rng.Intn(n)}
		}
		s, err := New(n, w)
		if err != nil {
			t.Fatal(err)
		}
		m := newNaive(n, w)

		dequeued := make(map[changeID]int)
		dups := make(map[changeID]int)
		writes := 0
		delivers := 0
		lastNow := make([]int64, n+1)

		// fullCompare 全量比对 ring 与朴素模拟的可见状态。
		fullCompare := func(step string) {
			for from := 1; from <= n; from++ {
				a, b := neighborsOf(n, from)
				for _, to := range []int{a, b} {
					got := s.Pending(from, to)
					want := m.queues[[2]int{from, to}]
					if len(got) != len(want) {
						t.Fatalf("iter=%d %s: Pending(%d,%d) 长度 %d != 朴素 %d", iter, step, from, to, len(got), len(want))
					}
					for i := range got {
						if got[i] != want[i] {
							t.Fatalf("iter=%d %s: Pending(%d,%d)[%d]=%v != 朴素 %v", iter, step, from, to, i, got[i], want[i])
						}
					}
				}
			}
			for site := 1; site <= n; site++ {
				for _, key := range validKeys {
					gotRec, gotOk := s.Get(site, key)
					wantRec, wantOk := m.winner(site, key)
					if gotOk != wantOk || gotRec != wantRec {
						t.Fatalf("iter=%d %s: Get(%d,%q)=(%+v,%v) != 朴素 (%+v,%v)",
							iter, step, site, key, gotRec, gotOk, wantRec, wantOk)
					}
				}
				for o := 1; o <= n; o++ {
					if got, want := s.Floor(site, o), m.floorPrefix(site, o); got != want {
						t.Fatalf("iter=%d %s: Floor(%d,%d)=%d != 朴素已见集合下限 %d",
							iter, step, site, o, got, want)
					}
				}
			}
		}

		doWrite := func(step int) {
			site := 1 + rng.Intn(n)
			key := validKeys[rng.Intn(len(validKeys))]
			switch rng.Intn(20) {
			case 0:
				key = ""
			case 1:
				key = string(make([]byte, 33))
			}
			op := change.Put
			switch rng.Intn(20) {
			case 0, 1, 2:
				op = change.Del
			case 3:
				op = change.Op(9)
			}
			val := int64(rng.Intn(1000))
			now := lastNow[site] + int64(rng.Intn(3))
			switch rng.Intn(30) {
			case 0:
				if lastNow[site] > 0 {
					now = lastNow[site] - 1 // 触发时钟回退
				}
			case 1:
				now = -1
			case 2:
				now = 1_000_000_000_001
			}
			gotRes, gotErr := s.Write(site, key, op, val, now)
			wantRes, wantErr := m.write(site, key, op, val, now)
			if gotRes != wantRes || !sameErr(gotErr, wantErr) {
				t.Fatalf("iter=%d step=%d: Write(%d,%q,%v,%d,%d)=(%v,%v) != 朴素 (%v,%v)",
					iter, step, site, key, op, val, now, gotRes, gotErr, wantRes, wantErr)
			}
			if gotErr == nil {
				writes++
				lastNow[site] = now
			}
		}

		doDeliver := func(step int) {
			from := 1 + rng.Intn(n)
			a, b := neighborsOf(n, from)
			to := a
			if rng.Intn(2) == 0 {
				to = b
			}
			if rng.Intn(10) == 0 {
				to = 1 + rng.Intn(n) // 可能不相邻，触发参数非法
			}
			var id changeID
			if head := s.Pending(from, to); len(head) > 0 {
				id = changeID{head[0].Origin, head[0].Seq}
			}
			gotRes, gotErr := s.Deliver(from, to)
			wantRes, wantErr := m.deliver(from, to)
			if gotRes != wantRes || !sameErr(gotErr, wantErr) {
				t.Fatalf("iter=%d step=%d: Deliver(%d,%d)=(%v,%v) != 朴素 (%v,%v)",
					iter, step, from, to, gotRes, gotErr, wantRes, wantErr)
			}
			if gotErr == nil {
				delivers++
				dequeued[id]++
				if gotRes == Dup {
					dups[id]++
				}
			}
		}

		doSetLink := func(step int) {
			from := 1 + rng.Intn(n)
			a, b := neighborsOf(n, from)
			to := a
			if rng.Intn(2) == 0 {
				to = b
			}
			if rng.Intn(10) == 0 {
				to = 1 + rng.Intn(n)
			}
			up := rng.Intn(2) == 0
			gotErr := s.SetLink(from, to, up)
			wantErr := m.setLink(from, to, up)
			if !sameErr(gotErr, wantErr) {
				t.Fatalf("iter=%d step=%d: SetLink(%d,%d,%v)=(%v) != 朴素 (%v)",
					iter, step, from, to, up, gotErr, wantErr)
			}
		}

		ops := 40 + rng.Intn(120)
		for step := 0; step < ops; step++ {
			switch r := rng.Intn(100); {
			case r < 45:
				doWrite(step)
			case r < 85:
				doDeliver(step)
			default:
				doSetLink(step)
			}
			fullCompare(fmt.Sprintf("step=%d", step))
		}

		// 恢复全部链路并排空。
		for from := 1; from <= n; from++ {
			a, b := neighborsOf(n, from)
			for _, to := range []int{a, b} {
				if err := s.SetLink(from, to, true); err != nil {
					t.Fatal(err)
				}
				m.setLink(from, to, true)
			}
		}
		for {
			var ready [][2]int
			for from := 1; from <= n; from++ {
				a, b := neighborsOf(n, from)
				for _, to := range []int{a, b} {
					if len(s.Pending(from, to)) > 0 {
						ready = append(ready, [2]int{from, to})
					}
				}
			}
			if len(ready) == 0 {
				break
			}
			pick := ready[rng.Intn(len(ready))]
			head := s.Pending(pick[0], pick[1])[0]
			id := changeID{head.Origin, head.Seq}
			gotRes, gotErr := s.Deliver(pick[0], pick[1])
			wantRes, wantErr := m.deliver(pick[0], pick[1])
			if gotRes != wantRes || !sameErr(gotErr, wantErr) {
				t.Fatalf("iter=%d drain: Deliver(%d,%d)=(%v,%v) != 朴素 (%v,%v)",
					iter, pick[0], pick[1], gotRes, gotErr, wantRes, wantErr)
			}
			delivers++
			dequeued[id]++
			if gotRes == Dup {
				dups[id]++
			}
		}
		fullCompare("drained")
		checkConverged(t, s, validKeys, writes, dequeued, dups)
		t.Logf("iter=%d 输入 n=%d W=%v ops=%d 输出 writes=%d delivers=%d 判定依据: 与朴素模拟逐步一致且收敛不变量满足",
			iter, n, w, ops, writes, delivers)
	}
}
