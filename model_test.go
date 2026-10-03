package ontology

// 朴素模拟：严格按规则逐步写成的参考实现，与 Syncer 对拍。

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/absent"
)

type mPart struct {
	epoch   int64
	open    bool
	seen    map[int64]bool
	vals    map[int64]int64
	abs     map[int64]int
	suspect bool
}

type model struct {
	P, K, X int
	maxNow  int64
	parts   []mPart

	lastN0 int // 最近一次 complete End 的 n0（判定依据）
	lastC  int // 最近一次 complete End 的 |C|
}

func newModel(p, k, x int) *model {
	m := &model{P: p, K: k, X: x, parts: make([]mPart, p)}
	for i := range m.parts {
		m.parts[i].vals = make(map[int64]int64)
		m.parts[i].abs = make(map[int64]int)
	}
	return m
}

func (m *model) checkNow(now int64) error {
	if now < 0 || now > maxClock {
		return ErrInvalid
	}
	if now < m.maxNow {
		return ErrClock
	}
	return nil
}

func (m *model) Begin(part int, now int64) (int64, error) {
	if part < 0 || part >= m.P {
		return 0, ErrInvalid
	}
	if err := m.checkNow(now); err != nil {
		return 0, err
	}
	p := &m.parts[part]
	if p.open {
		return 0, ErrBusy
	}
	p.open = true
	p.epoch++
	p.seen = make(map[int64]bool)
	m.maxNow = now
	return p.epoch, nil
}

func (m *model) Report(part int, epoch int64, rows []Row, now int64) ([]absent.Effect, error) {
	if part < 0 || part >= m.P {
		return nil, ErrInvalid
	}
	dup := make(map[int64]bool)
	for _, r := range rows {
		if r.K < 1 || r.K > maxKey || int(r.K%int64(m.P)) != part ||
			r.V < -maxValue || r.V > maxValue || dup[r.K] {
			return nil, ErrInvalid
		}
		dup[r.K] = true
	}
	if err := m.checkNow(now); err != nil {
		return nil, err
	}
	p := &m.parts[part]
	if !p.open || p.epoch != epoch {
		return nil, ErrNoSession
	}
	eff := make([]absent.Effect, len(rows))
	for i, r := range rows {
		old, ok := p.vals[r.K]
		switch {
		case !ok:
			eff[i] = absent.Insert
		case old != r.V:
			eff[i] = absent.Update
		default:
			eff[i] = absent.Same
		}
		p.vals[r.K] = r.V
		p.abs[r.K] = 0
		p.seen[r.K] = true
	}
	m.maxNow = now
	return eff, nil
}

func (m *model) End(part int, epoch int64, complete bool, now int64) (int, bool, error) {
	if part < 0 || part >= m.P {
		return 0, false, ErrInvalid
	}
	if err := m.checkNow(now); err != nil {
		return 0, false, err
	}
	p := &m.parts[part]
	if !p.open || p.epoch != epoch {
		return 0, false, ErrNoSession
	}
	seen := p.seen
	p.open = false
	p.seen = nil
	m.maxNow = now
	if !complete {
		return 0, false, nil
	}
	n0 := len(p.vals)
	for k := range p.vals {
		if !seen[k] {
			p.abs[k]++
		}
	}
	var c []int64
	for k, a := range p.abs {
		if a >= m.K {
			c = append(c, k)
		}
	}
	m.lastN0, m.lastC = n0, len(c)
	if p.suspect {
		return 0, false, nil
	}
	if int64(len(c))*100 > int64(m.X)*int64(n0) {
		p.suspect = true
		return 0, true, nil
	}
	for _, k := range c {
		delete(p.vals, k)
		delete(p.abs, k)
	}
	return len(c), false, nil
}

func (m *model) Approve(role, part int, now int64) (int, error) {
	if part < 0 || part >= m.P {
		return 0, ErrInvalid
	}
	if role != 2 {
		return 0, ErrPermission
	}
	if err := m.checkNow(now); err != nil {
		return 0, err
	}
	p := &m.parts[part]
	if !p.suspect {
		return 0, ErrNotSuspect
	}
	p.suspect = false
	m.maxNow = now
	n := 0
	for k, a := range p.abs {
		if a >= m.K {
			delete(p.vals, k)
			delete(p.abs, k)
			n++
		}
	}
	return n, nil
}

func (m *model) snapshot(part int) map[int64]Entry {
	p := &m.parts[part]
	out := make(map[int64]Entry, len(p.vals))
	for k, v := range p.vals {
		out[k] = Entry{V: v, A: p.abs[k]}
	}
	return out
}

func effString(e []absent.Effect) string {
	s := "["
	for i, x := range e {
		if i > 0 {
			s += " "
		}
		switch x {
		case absent.Insert:
			s += "Insert"
		case absent.Update:
			s += "Update"
		default:
			s += "Same"
		}
	}
	return s + "]"
}

// compareState 比较 Syncer 与模拟器的全部可观察状态。
func compareState(t *testing.T, trial, op int, s *Syncer, m *model) {
	t.Helper()
	for part := 0; part < m.P; part++ {
		got, want := s.Snapshot(part), m.snapshot(part)
		if len(got) != len(want) {
			t.Fatalf("trial=%d op=%d part=%d: |T| got %d want %d\n got=%v\nwant=%v",
				trial, op, part, len(got), len(want), got, want)
		}
		for k, w := range want {
			g, ok := got[k]
			if !ok || g != w {
				t.Fatalf("trial=%d op=%d part=%d key=%d: got %v(ok=%v) want %v",
					trial, op, part, k, g, ok, w)
			}
		}
		if s.Suspect(part) != m.parts[part].suspect {
			t.Fatalf("trial=%d op=%d part=%d: suspect got %v want %v",
				trial, op, part, s.Suspect(part), m.parts[part].suspect)
		}
		if s.Epoch(part) != m.parts[part].epoch {
			t.Fatalf("trial=%d op=%d part=%d: epoch got %d want %d",
				trial, op, part, s.Epoch(part), m.parts[part].epoch)
		}
	}
}

// TestDifferential 随机生成 2000 组操作序列，与朴素模拟逐步对拍，
// 日志打印每步输入、输出与判定依据。
func TestDifferential(t *testing.T) {
	const trials = 2000
	for trial := 0; trial < trials; trial++ {
		r := rand.New(rand.NewSource(int64(trial)*7919 + 17))
		p, k, x := 1+r.Intn(4), 1+r.Intn(3), 1+r.Intn(100)
		s, err := New(p, k, x)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		m := newModel(p, k, x)
		t.Logf("trial=%d 输入: P=%d K=%d X=%d", trial, p, k, x)
		now := int64(0)
		for op := 0; op < 40; op++ {
			now += int64(r.Intn(3))
			if r.Intn(25) == 0 && now >= 2 {
				now -= int64(r.Intn(3)) // 偶发时钟回退
			}
			part := r.Intn(p)
			if r.Intn(12) == 0 {
				part = []int{-1, p}[r.Intn(2)] // 偶发非法分区
			}
			switch dice := r.Intn(100); {
			case dice < 20:
				gEpoch, gErr := s.Begin(part, now)
				wEpoch, wErr := m.Begin(part, now)
				t.Logf("trial=%d op=%d Begin(part=%d,now=%d) => epoch=%d err=%v (want epoch=%d err=%v)",
					trial, op, part, now, gEpoch, gErr, wEpoch, wErr)
				if gEpoch != wEpoch || gErr != wErr {
					t.Fatalf("Begin mismatch: got (%d,%v) want (%d,%v)", gEpoch, gErr, wEpoch, wErr)
				}
			case dice < 55:
				var epoch int64
				if part >= 0 && part < p && m.parts[part].open && r.Intn(10) > 0 {
					epoch = m.parts[part].epoch
				} else {
					epoch = int64(1 + r.Intn(3))
				}
				n := r.Intn(5)
				rows := make([]Row, 0, n)
				for i := 0; i < n; i++ {
					var key int64
					if part >= 0 && part < p && r.Intn(10) > 0 {
						key = int64(part) + int64(p)*(1+r.Int63n(4)) // 属于该分区的键
						if key == 0 {
							key = int64(p)
						}
					} else {
						key = 1 + r.Int63n(12) // 可能不属于该分区
					}
					if r.Intn(10) == 0 && len(rows) > 0 {
						key = rows[r.Intn(len(rows))].K // 偶发重复键
					}
					v := r.Int63n(2000) - 1000
					if r.Intn(20) == 0 {
						v = maxValue + 1 // 偶发越界值
					}
					rows = append(rows, Row{K: key, V: v})
				}
				gEff, gErr := s.Report(part, epoch, rows, now)
				wEff, wErr := m.Report(part, epoch, rows, now)
				t.Logf("trial=%d op=%d Report(part=%d,epoch=%d,rows=%v,now=%d) => %s err=%v (want %s err=%v)",
					trial, op, part, epoch, rows, now, effString(gEff), gErr, effString(wEff), wErr)
				if gErr != wErr || len(gEff) != len(wEff) {
					t.Fatalf("Report mismatch: got (%v,%v) want (%v,%v)", gEff, gErr, wEff, wErr)
				}
				for i := range gEff {
					if gEff[i] != wEff[i] {
						t.Fatalf("Report effect[%d] mismatch: got %v want %v", i, gEff[i], wEff[i])
					}
				}
			case dice < 80:
				var epoch int64
				if part >= 0 && part < p && m.parts[part].open && r.Intn(10) > 0 {
					epoch = m.parts[part].epoch
				} else {
					epoch = int64(1 + r.Intn(3))
				}
				complete := r.Intn(4) != 0
				gDel, gTrip, gErr := s.End(part, epoch, complete, now)
				wDel, wTrip, wErr := m.End(part, epoch, complete, now)
				t.Logf("trial=%d op=%d End(part=%d,epoch=%d,complete=%v,now=%d) => del=%d tripped=%v err=%v (want del=%d tripped=%v err=%v) 判定依据: n0=%d |C|=%d |C|*100=%d X*n0=%d suspect=%v",
					trial, op, part, epoch, complete, now, gDel, gTrip, gErr, wDel, wTrip, wErr,
					m.lastN0, m.lastC, m.lastC*100, x*m.lastN0, part >= 0 && part < p && m.parts[part].suspect)
				if gDel != wDel || gTrip != wTrip || gErr != wErr {
					t.Fatalf("End mismatch: got (%d,%v,%v) want (%d,%v,%v)", gDel, gTrip, gErr, wDel, wTrip, wErr)
				}
			default:
				role := 2
				if r.Intn(5) == 0 {
					role = r.Intn(3)
				}
				gDel, gErr := s.Approve(role, part, now)
				wDel, wErr := m.Approve(role, part, now)
				t.Logf("trial=%d op=%d Approve(role=%d,part=%d,now=%d) => del=%d err=%v (want del=%d err=%v)",
					trial, op, role, part, now, gDel, gErr, wDel, wErr)
				if gDel != wDel || gErr != wErr {
					t.Fatalf("Approve mismatch: got (%d,%v) want (%d,%v)", gDel, gErr, wDel, wErr)
				}
			}
			compareState(t, trial, op, s, m)
		}
		t.Logf("trial=%d 输出一致: 40 步操作结果与最终状态(T/a/Suspect/epoch)全部相同", trial)
	}
}

// TestConcurrentPartitions 多分区并发交错，每分区操作序列确定，
// 最终结果须与串行推演一致（配合 -race 检测数据竞争）。
func TestConcurrentPartitions(t *testing.T) {
	const p = 4
	s, err := New(p, 2, 100)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 所有操作使用同一 now=1（now 允许等于 maxNow），避免时间戳分配顺序
	// 与操作接受顺序不一致导致的非确定性时钟回退。
	const now = int64(1)
	var wg sync.WaitGroup
	errs := make(chan error, p*8)
	for part := 0; part < p; part++ {
		part := part
		wg.Add(1)
		go func() {
			defer wg.Done()
			keys := [3]int64{int64(part) + p, int64(part) + 2*p, int64(part) + 3*p}
			for round := 0; round < 4; round++ {
				epoch, err := s.Begin(part, now)
				if err != nil {
					errs <- fmt.Errorf("part=%d round=%d Begin: %w", part, round, err)
					return
				}
				report := keys[:]
				if round >= 2 {
					report = keys[:2] // 后两轮缺席第三个键
				}
				rows := make([]Row, len(report))
				for i, k := range report {
					rows[i] = Row{K: k, V: int64(round)}
				}
				if _, err := s.Report(part, epoch, rows, now); err != nil {
					errs <- fmt.Errorf("part=%d round=%d Report: %w", part, round, err)
					return
				}
				del, tripped, err := s.End(part, epoch, true, now)
				if err != nil {
					errs <- fmt.Errorf("part=%d round=%d End: %w", part, round, err)
					return
				}
				if tripped {
					errs <- fmt.Errorf("part=%d round=%d: unexpected trip (X=100)", part, round)
					return
				}
				wantDel := 0
				if round == 3 {
					wantDel = 1 // 第三轮缺席的键在第四轮 a=2=K 被删
				}
				if del != wantDel {
					errs <- fmt.Errorf("part=%d round=%d: deleted=%d want %d", part, round, del, wantDel)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	for part := 0; part < p; part++ {
		snap := s.Snapshot(part)
		if len(snap) != 2 {
			t.Errorf("part=%d: final |T|=%d want 2 (%v)", part, len(snap), snap)
		}
		for k, e := range snap {
			if e.A != 0 || e.V != 3 {
				t.Errorf("part=%d key=%d: got %+v want {V:3 A:0}", part, k, e)
			}
		}
		if s.Suspect(part) {
			t.Errorf("part=%d: unexpected suspect", part)
		}
		if s.Epoch(part) != 4 {
			t.Errorf("part=%d: epoch=%d want 4", part, s.Epoch(part))
		}
	}
}
