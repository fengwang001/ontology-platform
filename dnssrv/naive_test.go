package dnssrv

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// naiveRec is a deliberately simple, independently written reference record.
type naiveRec struct {
	target    string
	port      int
	priority  int
	weight    int
	expire    int64
	regID     int64
	coolUntil int64
	f         int
	lf        int64
	hasLF     bool
}

// naiveModel re-implements every rule step by step, with no shared code with
// the production Selector.
type naiveModel struct {
	cap     int
	coolCap int64
	wr      int64
	nextID  int64
	recs    map[string]*naiveRec
}

func newNaive(capacity int, coolCap, wr int64) *naiveModel {
	return &naiveModel{
		cap: capacity, coolCap: coolCap, wr: wr,
		nextID: 1, recs: map[string]*naiveRec{},
	}
}

func nkey(t string, p int) string { return fmt.Sprintf("%s\x00%d", t, p) }

func validTime(now int64) bool { return now >= 0 && now <= 1_000_000_000_000_000 }

func (m *naiveModel) add(t string, p, pri, w int, ttl, now int64) error {
	if t == "" || p < 1 || p > 65535 || pri < 0 || pri > 65535 ||
		w < 0 || w > 65535 || ttl < 1 || ttl > 1_000_000_000 {
		return ErrInvalidArg
	}
	if !validTime(now) {
		return ErrInvalidTime
	}
	k := nkey(t, p)
	if r := m.recs[k]; r != nil {
		r.priority = pri
		r.weight = w
		r.expire = now + ttl
		return nil
	}
	if len(m.recs) >= m.cap {
		for kk, r := range m.recs {
			if r.expire <= now {
				delete(m.recs, kk)
			}
		}
		if len(m.recs) >= m.cap {
			return ErrFull
		}
	}
	m.recs[k] = &naiveRec{
		target: t, port: p, priority: pri, weight: w,
		expire: now + ttl, regID: m.nextID,
	}
	m.nextID++
	return nil
}

func (m *naiveModel) fail(t string, p int, cd, now int64) error {
	if t == "" || cd < 1 || cd > 1_000_000_000 {
		return ErrInvalidArg
	}
	if !validTime(now) {
		return ErrInvalidTime
	}
	r := m.recs[nkey(t, p)]
	if r == nil {
		return ErrNotFound
	}
	if now >= r.expire {
		return ErrExpired
	}
	if !r.hasLF || now >= r.lf+m.wr {
		r.f = 0
	}
	r.f++
	r.lf = now
	r.hasLF = true
	shift := r.f - 1
	if shift > 30 {
		shift = 30
	}
	eff := cd * (int64(1) << uint(shift))
	if eff > m.coolCap {
		eff = m.coolCap
	}
	if d := now + eff; d > r.coolUntil {
		r.coolUntil = d
	}
	return nil
}

func (m *naiveModel) success(t string, p int, now int64) error {
	if t == "" {
		return ErrInvalidArg
	}
	if !validTime(now) {
		return ErrInvalidTime
	}
	r := m.recs[nkey(t, p)]
	if r == nil {
		return ErrNotFound
	}
	if now >= r.expire {
		return ErrExpired
	}
	r.f = 0
	return nil
}

func (m *naiveModel) pick(rr uint64, now int64) (string, int, error) {
	if !validTime(now) {
		return "", 0, ErrInvalidTime
	}
	var avail []*naiveRec
	for _, r := range m.recs {
		if now < r.expire && now >= r.coolUntil {
			avail = append(avail, r)
		}
	}
	if len(avail) == 0 {
		return "", 0, ErrNoAvailable
	}
	minPri := avail[0].priority
	for _, r := range avail {
		if r.priority < minPri {
			minPri = r.priority
		}
	}
	var grp []*naiveRec
	for _, r := range avail {
		if r.priority == minPri {
			grp = append(grp, r)
		}
	}
	// Explicit two-list ordering: zero-weight regIDs first, then weighted.
	var zeros, weighted []*naiveRec
	for _, r := range grp {
		if r.weight == 0 {
			zeros = append(zeros, r)
		} else {
			weighted = append(weighted, r)
		}
	}
	sort.Slice(zeros, func(i, j int) bool { return zeros[i].regID < zeros[j].regID })
	sort.Slice(weighted, func(i, j int) bool { return weighted[i].regID < weighted[j].regID })
	ordered := append(zeros, weighted...)
	var s int64
	for _, r := range ordered {
		s += int64(r.weight)
	}
	r1 := int64(rr % (uint64(s) + 1))
	var cum int64
	for _, r := range ordered {
		cum += int64(r.weight)
		if cum >= r1 {
			return r.target, r.port, nil
		}
	}
	panic("naive: unreachable")
}

func (m *naiveModel) purge(now int64) (int, error) {
	if !validTime(now) {
		return 0, ErrInvalidTime
	}
	n := 0
	for k, r := range m.recs {
		if r.expire <= now {
			delete(m.recs, k)
			n++
		}
	}
	return n, nil
}

func sameState(s *Selector, m *naiveModel) string {
	if len(s.recs) != len(m.recs) {
		return fmt.Sprintf("len differs: %d vs %d", len(s.recs), len(m.recs))
	}
	if s.nextID != m.nextID {
		return fmt.Sprintf("nextID differs: %d vs %d", s.nextID, m.nextID)
	}
	for k, r := range s.recs {
		nr := m.recs[nkey(k.target, k.port)]
		if nr == nil {
			return fmt.Sprintf("record %s missing in naive", nkey(k.target, k.port))
		}
		if r.priority != nr.priority || r.weight != nr.weight ||
			r.expire != nr.expire || r.regID != nr.regID ||
			r.coolUntil != nr.coolUntil || r.f != nr.f ||
			r.hasLF != nr.hasLF || (r.hasLF && r.lf != nr.lf) {
			return fmt.Sprintf("record %s differs:\n real=%+v\nnaive=%+v",
				nkey(k.target, k.port), r, nr)
		}
	}
	return ""
}

// TestDifferentialRandom runs 2000 random record sets with random add /
// failure / success / purge / pick sequences against the naive model. Inputs,
// outputs and the decision basis are logged; both errors and full internal
// state must agree after every step.
func TestDifferentialRandom(t *testing.T) {
	const scenarios = 2000
	for sc := 0; sc < scenarios; sc++ {
		rng := rand.New(rand.NewSource(int64(sc)*7919 + 1234567))
		capacity := 1 + rng.Intn(6)
		coolCap := int64(1 + rng.Intn(200))
		wr := int64(1 + rng.Intn(300))
		sel, err := New(capacity, coolCap, wr)
		if err != nil {
			t.Fatalf("scenario %d: New: %v", sc, err)
		}
		naive := newNaive(capacity, coolCap, wr)

		nOps := 1 + rng.Intn(60)
		var now int64
		for op := 0; op < nOps; op++ {
			// Non-decreasing time, with occasional jumps and occasional
			// invalid times to exercise rejection ordering.
			switch rng.Intn(10) {
			case 0:
				now += int64(rng.Intn(400))
			case 1:
				// no advance
			default:
				now += int64(rng.Intn(30))
			}

			switch rng.Intn(5) {
			case 0: // Add, sometimes deliberately invalid
				target := fmt.Sprintf("h%d", rng.Intn(8))
				if rng.Intn(20) == 0 {
					target = ""
				}
				port := 1 + rng.Intn(65535)
				if rng.Intn(20) == 0 {
					port = 0
				}
				pri := rng.Intn(65536)
				w := rng.Intn(65536)
				ttl := int64(1 + rng.Intn(500))
				e1 := sel.Add(target, port, pri, w, ttl, now)
				e2 := naive.add(target, port, pri, w, ttl, now)
				t.Logf("[%d.%d] 输入 Add(%s,%d p=%d w=%d ttl=%d now=%d) 输出 real=%v naive=%v 判定:参数与时间校验+满容量淘汰",
					sc, op, target, port, pri, w, ttl, now, e1, e2)
				if !sameErr(e1, e2) {
					t.Fatalf("Add error differs: %v vs %v", e1, e2)
				}
			case 1, 2: // Failure on a random (often existing) id
				target := fmt.Sprintf("h%d", rng.Intn(8))
				cd := int64(1 + rng.Intn(300))
				if rng.Intn(20) == 0 {
					cd = 0
				}
				port := 1 + rng.Intn(10)
				e1 := sel.Failure(target, port, cd, now)
				e2 := naive.fail(target, port, cd, now)
				t.Logf("[%d.%d] 输入 Failure(%s,%d cd=%d now=%d) 输出 real=%v naive=%v 判定:静默期清零+指数冷却+max",
					sc, op, target, port, cd, now, e1, e2)
				if !sameErr(e1, e2) {
					t.Fatalf("Failure error differs: %v vs %v", e1, e2)
				}
			case 3: // Success
				target := fmt.Sprintf("h%d", rng.Intn(8))
				port := 1 + rng.Intn(10)
				e1 := sel.Success(target, port, now)
				e2 := naive.success(target, port, now)
				t.Logf("[%d.%d] 输入 Success(%s,%d now=%d) 输出 real=%v naive=%v 判定:仅清 f",
					sc, op, target, port, now, e1, e2)
				if !sameErr(e1, e2) {
					t.Fatalf("Success error differs: %v vs %v", e1, e2)
				}
			case 4: // Pick with random r, including 0 and large values
				var rr uint64
				switch rng.Intn(4) {
				case 0:
					rr = 0
				case 1:
					rr = uint64(now)
				default:
					rr = rng.Uint64()
				}
				t1, p1, e1 := sel.Pick(rr, now)
				t2, p2, e2 := naive.pick(rr, now)
				t.Logf("[%d.%d] 输入 Pick(r=%d now=%d) 输出 real=(%s,%d,%v) naive=(%s,%d,%v) 判定:最低优先组+排序+累计>=r mod(S+1)",
					sc, op, rr, now, t1, p1, e1, t2, p2, e2)
				if !sameErr(e1, e2) || t1 != t2 || p1 != p2 {
					t.Fatalf("Pick differs: (%s,%d,%v) vs (%s,%d,%v)",
						t1, p1, e1, t2, p2, e2)
				}
			}

			// Purge occasionally.
			if rng.Intn(6) == 0 {
				n1, e1 := sel.Purge(now)
				n2, e2 := naive.purge(now)
				t.Logf("[%d.%d] 输入 Purge(now=%d) 输出 real=(%d,%v) naive=(%d,%v) 判定:删除 expire<=now",
					sc, op, now, n1, e1, n2, e2)
				if n1 != n2 || !sameErr(e1, e2) {
					t.Fatalf("Purge differs: (%d,%v) vs (%d,%v)", n1, e1, n2, e2)
				}
			}

			if diff := sameState(sel, naive); diff != "" {
				t.Fatalf("scenario %d op %d state mismatch:\n%s", sc, op, diff)
			}
		}
	}
}

func sameErr(a, b error) bool {
	return errors.Is(a, b) && errors.Is(b, a)
}

// TestConcurrent hammers all operations concurrently; the race detector
// verifies memory safety, and afterwards invariants still hold.
func TestConcurrent(t *testing.T) {
	s := mustNew(t, 32, 60, 100)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 500; i++ {
				now := int64(rng.Intn(2000))
				tgt := fmt.Sprintf("h%d", rng.Intn(40))
				port := 1 + rng.Intn(20)
				switch rng.Intn(5) {
				case 0:
					_ = s.Add(tgt, port, rng.Intn(5), rng.Intn(20),
						int64(1+rng.Intn(300)), now)
				case 1:
					_ = s.Failure(tgt, port, int64(1+rng.Intn(50)), now)
				case 2:
					_ = s.Success(tgt, port, now)
				case 3:
					_, _, _ = s.Pick(rng.Uint64(), now)
				case 4:
					_, _ = s.Purge(now)
				}
			}
		}(int64(g) + 1)
	}
	wg.Wait()
	if len(s.recs) > s.cap {
		t.Fatalf("capacity exceeded: %d > %d", len(s.recs), s.cap)
	}
	for _, r := range s.recs {
		if r.regID <= 0 || r.f < 0 || r.coolUntil < 0 {
			t.Fatalf("invariant broken: %+v", r)
		}
	}
	t.Logf("判定依据: 8 goroutine x 500 操作并发完成, 记录数=%d <= Cap=%d, 序号/冷却不变量成立",
		len(s.recs), s.cap)
}
