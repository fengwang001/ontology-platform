package restart

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// 朴素模拟：按规范逐条规则用线性扫描实现，与 Manager 逐步对照。

type simOwner struct {
	stale    bool
	deadline int64
}

type simBind struct {
	label  int
	owners map[uint32]simOwner
}

type simQuar struct {
	release int64
	fec     string
}

type sim struct {
	lo, hi int
	hd, r  int64
	q      int
	maxNow int64
	bnds   map[string]*simBind
	quar   map[int]simQuar
	aff    map[string]int
	st     map[uint32]int // 0 正常 1 离线 2 恢复中
	cnt    map[uint32]int
	log    []LogEntry
}

func newSim(lo, hi int, hd, r int64, q int) *sim {
	return &sim{
		lo: lo, hi: hi, hd: hd, r: r, q: q,
		bnds: map[string]*simBind{},
		quar: map[int]simQuar{},
		aff:  map[string]int{},
		st:   map[uint32]int{},
		cnt:  map[uint32]int{},
	}
}

func simValid(fec []byte, c uint32, now int64) bool {
	return len(fec) >= 1 && len(fec) <= 64 && c >= 1 && c <= 10000 && now >= 0 && now <= 1e12
}

type simUndo struct {
	fec    string
	client uint32
	owner  simOwner
	freed  bool
	label  int
}

// land 落地全部截止时刻不大于 now 的到期事件，返回撤销函数（被拒绝操作用）。
func (s *sim) land(now int64) (undo func()) {
	type ev struct {
		d   int64
		fec string
		c   uint32
	}
	var evs []ev
	for f, b := range s.bnds {
		for c, o := range b.owners {
			if o.stale && o.deadline <= now {
				evs = append(evs, ev{o.deadline, f, c})
			}
		}
	}
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].d != evs[j].d {
			return evs[i].d < evs[j].d
		}
		if evs[i].fec != evs[j].fec {
			return evs[i].fec < evs[j].fec
		}
		return evs[i].c < evs[j].c
	})
	var undos []simUndo
	for _, e := range evs {
		b := s.bnds[e.fec]
		o := b.owners[e.c]
		delete(b.owners, e.c)
		if e.c != 0 {
			s.cnt[e.c]--
		}
		u := simUndo{fec: e.fec, client: e.c, owner: o}
		if len(b.owners) == 0 {
			u.freed = true
			u.label = b.label
			delete(s.bnds, e.fec)
			s.quar[b.label] = simQuar{e.d, e.fec}
			s.aff[e.fec] = b.label
			s.log = append(s.log, LogEntry{OpFree, e.fec, b.label, e.d})
		}
		undos = append(undos, u)
	}
	return func() {
		for i := len(undos) - 1; i >= 0; i-- {
			u := undos[i]
			if u.freed {
				delete(s.quar, u.label)
				if s.aff[u.fec] == u.label {
					delete(s.aff, u.fec)
				}
				s.bnds[u.fec] = &simBind{label: u.label, owners: map[uint32]simOwner{}}
				s.log = s.log[:len(s.log)-1]
			}
			s.bnds[u.fec].owners[u.client] = u.owner
			if u.client != 0 {
				s.cnt[u.client]++
			}
		}
	}
}

func (s *sim) alloc(f string, now int64) (int, bool) {
	for l, q := range s.quar {
		if q.release+s.hd <= now {
			delete(s.quar, l)
			if s.aff[q.fec] == l {
				delete(s.aff, q.fec)
			}
		}
	}
	if l, ok := s.aff[f]; ok {
		if _, q := s.quar[l]; q {
			delete(s.quar, l)
			delete(s.aff, f)
			return l, true
		}
		delete(s.aff, f)
	}
	used := map[int]bool{}
	for _, b := range s.bnds {
		used[b.label] = true
	}
	for l := s.lo; l <= s.hi; l++ {
		if used[l] {
			continue
		}
		if _, q := s.quar[l]; q {
			continue
		}
		return l, true
	}
	return 0, false
}

func (s *sim) Bind(fec []byte, client uint32, now int64) (int, error) {
	if !simValid(fec, client, now) {
		return 0, ErrInvalid
	}
	if now < s.maxNow {
		return 0, ErrClock
	}
	if s.st[client] == 1 {
		return 0, ErrClientOffline
	}
	undo := s.land(now)
	f := string(fec)
	if b, ok := s.bnds[f]; ok {
		if o, ok := b.owners[client]; ok {
			if o.stale {
				b.owners[client] = simOwner{}
			}
			s.maxNow = now
			return b.label, nil
		}
	}
	if s.cnt[client] >= s.q {
		undo()
		return 0, ErrOwnerLimit
	}
	if b, ok := s.bnds[f]; ok {
		delete(b.owners, 0)
		b.owners[client] = simOwner{}
		s.cnt[client]++
		s.maxNow = now
		return b.label, nil
	}
	label, ok := s.alloc(f, now)
	if !ok {
		undo()
		return 0, ErrExhausted
	}
	s.bnds[f] = &simBind{label, map[uint32]simOwner{client: {}}}
	s.cnt[client]++
	s.log = append(s.log, LogEntry{OpAlloc, f, label, now})
	s.maxNow = now
	return label, nil
}

func (s *sim) Unbind(fec []byte, client uint32, now int64) error {
	if !simValid(fec, client, now) {
		return ErrInvalid
	}
	if now < s.maxNow {
		return ErrClock
	}
	if s.st[client] == 1 {
		return ErrClientOffline
	}
	undo := s.land(now)
	f := string(fec)
	b, ok := s.bnds[f]
	if !ok {
		undo()
		return ErrNoBinding
	}
	if _, ok := b.owners[client]; !ok {
		undo()
		return ErrNoBinding
	}
	delete(b.owners, client)
	s.cnt[client]--
	if len(b.owners) == 0 {
		delete(s.bnds, f)
		s.quar[b.label] = simQuar{now, f}
		s.aff[f] = b.label
		s.log = append(s.log, LogEntry{OpFree, f, b.label, now})
	}
	s.maxNow = now
	return nil
}

func (s *sim) ClientDown(client uint32, now int64) error {
	if client < 1 || client > 10000 || now < 0 || now > 1e12 {
		return ErrInvalid
	}
	if now < s.maxNow {
		return ErrClock
	}
	if s.st[client] == 1 {
		return ErrState
	}
	s.land(now)
	for _, b := range s.bnds {
		if o, ok := b.owners[client]; ok && !o.stale {
			b.owners[client] = simOwner{true, now + s.r}
		}
	}
	s.st[client] = 1
	s.maxNow = now
	return nil
}

func (s *sim) ClientUp(client uint32, now int64) error {
	if client < 1 || client > 10000 || now < 0 || now > 1e12 {
		return ErrInvalid
	}
	if now < s.maxNow {
		return ErrClock
	}
	if s.st[client] != 1 {
		return ErrState
	}
	s.land(now)
	s.st[client] = 2
	s.maxNow = now
	return nil
}

func (s *sim) EndOfRib(client uint32, now int64) error {
	if client < 1 || client > 10000 || now < 0 || now > 1e12 {
		return ErrInvalid
	}
	if now < s.maxNow {
		return ErrClock
	}
	if s.st[client] == 1 {
		return ErrClientOffline
	}
	if s.st[client] != 2 {
		return ErrState
	}
	s.land(now)
	fecs := []string{}
	for f, b := range s.bnds {
		if o, ok := b.owners[client]; ok && o.stale {
			fecs = append(fecs, f)
		}
	}
	sort.Strings(fecs)
	for _, f := range fecs {
		b := s.bnds[f]
		delete(b.owners, client)
		s.cnt[client]--
		if len(b.owners) == 0 {
			delete(s.bnds, f)
			s.quar[b.label] = simQuar{now, f}
			s.aff[f] = b.label
			s.log = append(s.log, LogEntry{OpFree, f, b.label, now})
		}
	}
	s.st[client] = 0
	s.maxNow = now
	return nil
}

func (s *sim) Restore(log []LogEntry, now int64) error {
	if now < 0 || now > 1e12 {
		return ErrInvalid
	}
	var maxT int64
	for _, e := range log {
		if e.Op != OpAlloc && e.Op != OpFree {
			return ErrInvalid
		}
		if len(e.Fec) < 1 || len(e.Fec) > 64 || e.Label < s.lo || e.Label > s.hi ||
			e.Time < 0 || e.Time > 1e12 {
			return ErrInvalid
		}
		if e.Time > maxT {
			maxT = e.Time
		}
	}
	if now < maxT {
		return ErrClock
	}
	ns := newSim(s.lo, s.hi, s.hd, s.r, s.q)
	for _, e := range log {
		switch e.Op {
		case OpAlloc:
			if _, ok := ns.bnds[e.Fec]; ok {
				return ErrCorrupt
			}
			if q, ok := ns.quar[e.Label]; ok {
				delete(ns.quar, e.Label)
				if ns.aff[q.fec] == e.Label {
					delete(ns.aff, q.fec)
				}
			} else {
				for _, b := range ns.bnds {
					if b.label == e.Label {
						return ErrCorrupt
					}
				}
			}
			ns.bnds[e.Fec] = &simBind{e.Label, map[uint32]simOwner{}}
		case OpFree:
			b, ok := ns.bnds[e.Fec]
			if !ok || b.label != e.Label {
				return ErrCorrupt
			}
			delete(ns.bnds, e.Fec)
			ns.quar[e.Label] = simQuar{e.Time, e.Fec}
			ns.aff[e.Fec] = e.Label
		}
	}
	for _, b := range ns.bnds {
		b.owners[0] = simOwner{true, now + s.r}
	}
	ns.log = append([]LogEntry(nil), log...)
	ns.maxNow = now
	*s = *ns
	return nil
}

func (s *sim) mapping() map[string]int {
	out := map[string]int{}
	for f, b := range s.bnds {
		out[f] = b.label
	}
	return out
}

// 1500 组随机操作序列与朴素模拟逐步对照：返回值、错误（errors.Is）、
// 日志与 fec→标签映射必须逐步一致；日志中打印输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	for seed := int64(0); seed < 1500; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			lo := 16
			hi := lo + 7 + rng.Intn(8) // 8..15 个标签，迫使耗尽与复用
			hd := int64(rng.Intn(81))
			r := int64(rng.Intn(81))
			q := 1 + rng.Intn(4)
			m, err := New(lo, hi, hd, r, q)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			s := newSim(lo, hi, hd, r, q)
			t.Logf("配置: lo=%d hi=%d hd=%d r=%d q=%d", lo, hi, hd, r, q)

			now := int64(0)
			ops := 30 + rng.Intn(30)
			for i := 0; i < ops; i++ {
				switch rng.Intn(10) {
				case 0: // 可能时钟回退
					now -= int64(rng.Intn(30))
					if now < 0 {
						now = 0
					}
				case 1: // 时刻不变，撞恰等边界
				default:
					now += int64(rng.Intn(60))
				}
				fec := fmt.Sprintf("f%d", rng.Intn(10))
				switch rng.Intn(40) {
				case 0:
					fec = strings.Repeat("x", 64)
				case 1:
					fec = ""
				}
				client := uint32(1 + rng.Intn(5))
				if rng.Intn(40) == 0 {
					client = 0
				}

				kind := rng.Intn(100)
				var desc, gotS, wantS string
				switch {
				case kind < 45:
					gotL, gotErr := m.Bind([]byte(fec), client, now)
					wantL, wantErr := s.Bind([]byte(fec), client, now)
					desc = fmt.Sprintf("Bind(%q,%d,%d)", fec, client, now)
					gotS, wantS = fmt.Sprintf("(%d,%v)", gotL, gotErr), fmt.Sprintf("(%d,%v)", wantL, wantErr)
					if !sameErr(gotErr, wantErr) || (gotErr == nil && gotL != wantL) {
						t.Fatalf("op %d %s: got=%s want=%s", i, desc, gotS, wantS)
					}
				case kind < 65:
					gotErr := m.Unbind([]byte(fec), client, now)
					wantErr := s.Unbind([]byte(fec), client, now)
					desc = fmt.Sprintf("Unbind(%q,%d,%d)", fec, client, now)
					gotS, wantS = fmt.Sprint(gotErr), fmt.Sprint(wantErr)
					if !sameErr(gotErr, wantErr) {
						t.Fatalf("op %d %s: got=%s want=%s", i, desc, gotS, wantS)
					}
				case kind < 75:
					gotErr := m.ClientDown(client, now)
					wantErr := s.ClientDown(client, now)
					desc = fmt.Sprintf("ClientDown(%d,%d)", client, now)
					gotS, wantS = fmt.Sprint(gotErr), fmt.Sprint(wantErr)
					if !sameErr(gotErr, wantErr) {
						t.Fatalf("op %d %s: got=%s want=%s", i, desc, gotS, wantS)
					}
				case kind < 83:
					gotErr := m.ClientUp(client, now)
					wantErr := s.ClientUp(client, now)
					desc = fmt.Sprintf("ClientUp(%d,%d)", client, now)
					gotS, wantS = fmt.Sprint(gotErr), fmt.Sprint(wantErr)
					if !sameErr(gotErr, wantErr) {
						t.Fatalf("op %d %s: got=%s want=%s", i, desc, gotS, wantS)
					}
				case kind < 90:
					gotErr := m.EndOfRib(client, now)
					wantErr := s.EndOfRib(client, now)
					desc = fmt.Sprintf("EndOfRib(%d,%d)", client, now)
					gotS, wantS = fmt.Sprint(gotErr), fmt.Sprint(wantErr)
					if !sameErr(gotErr, wantErr) {
						t.Fatalf("op %d %s: got=%s want=%s", i, desc, gotS, wantS)
					}
				default:
					log := m.Log()
					gotErr := m.Restore(log, now)
					wantErr := s.Restore(log, now)
					desc = fmt.Sprintf("Restore(len=%d,%d)", len(log), now)
					gotS, wantS = fmt.Sprint(gotErr), fmt.Sprint(wantErr)
					if !sameErr(gotErr, wantErr) {
						t.Fatalf("op %d %s: got=%s want=%s", i, desc, gotS, wantS)
					}
				}
				// 判定依据：日志与映射逐步一致。
				logOK := reflect.DeepEqual(m.Log(), s.log)
				mapOK := reflect.DeepEqual(mappingOf(m), s.mapping())
				t.Logf("op %d: %s → got=%s want=%s 日志一致=%v 映射一致=%v",
					i, desc, gotS, wantS, logOK, mapOK)
				if !logOK {
					t.Fatalf("op %d %s: log\n got=%v\nwant=%v", i, desc, m.Log(), s.log)
				}
				if !mapOK {
					t.Fatalf("op %d %s: mapping\n got=%v\nwant=%v", i, desc, mappingOf(m), s.mapping())
				}
			}
		})
	}
}

func sameErr(got, want error) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return errors.Is(got, want) && errors.Is(want, got)
}
