package traffic

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

type naiveBucket struct {
	state int
	owner string
	rel   int64
	k     int
}

type naiveModel struct {
	b, h   int
	cd, p  int64
	bk     []naiveBucket
	exps   map[string][2]int
	g      int64
	maxNow int64
}

func naiveNew(b, h int, cd, p int64) *naiveModel {
	return &naiveModel{b: b, h: h, cd: cd, p: p,
		bk:   make([]naiveBucket, b),
		exps: map[string][2]int{}}
}

func (m *naiveModel) mult(k int) int64 {
	if k > 3 {
		return 3
	}
	if k < 1 {
		return 1
	}
	return int64(k)
}

// avail 逐桶照抄规则。
func (m *naiveModel) avail(i int, id string, now int64) bool {
	x := m.bk[i]
	switch x.state {
	case StateFree:
		return true
	case StateCooldown:
		if x.owner == id {
			return true
		}
		return now >= x.rel+m.cd*m.mult(x.k)
	default:
		return false
	}
}

func validNaiveNow(now int64) bool { return now >= 0 && now <= MaxNow }

func (m *naiveModel) claim(id string, n int, now int64) (int, string) {
	if id == "" || n < 1 || n > m.b-m.h || !validNaiveNow(now) {
		return 0, "invalid"
	}
	if _, ok := m.exps[id]; ok {
		return 0, "exists"
	}
	if now < m.maxNow {
		return 0, "rewind"
	}
	start := -1
	for i := m.h; i <= m.b-n; i++ {
		ok := true
		for j := 0; j < n; j++ {
			if !m.avail(i+j, id, now) {
				ok = false
				break
			}
		}
		if ok {
			start = i
			break
		}
	}
	if start < 0 {
		return 0, "capacity"
	}
	for i := start; i < start+n; i++ {
		m.bk[i].state = StateHeld
		m.bk[i].owner = id
	}
	m.exps[id] = [2]int{start, start + n}
	m.maxNow = now
	return start, ""
}

func (m *naiveModel) resize(id string, n2 int, now int64) (int, string) {
	if id == "" || n2 < 1 || n2 > m.b-m.h || !validNaiveNow(now) {
		return 0, "invalid"
	}
	rng, ok := m.exps[id]
	if !ok {
		return 0, "missing"
	}
	if now < m.maxNow {
		return 0, "rewind"
	}
	s, e := rng[0], rng[1]
	cnt := e - s
	switch {
	case n2 == cnt:
		m.maxNow = now
		return s, ""
	case n2 < cnt:
		for i := s + n2; i < e; i++ {
			m.bk[i].state = StateCooldown
			m.bk[i].owner = id
			m.bk[i].rel = now
			m.bk[i].k++
		}
		m.exps[id] = [2]int{s, s + n2}
		m.maxNow = now
		return s, ""
	default:
		need := n2 - cnt
		if e+need > m.b {
			return 0, "capacity"
		}
		for i := e; i < e+need; i++ {
			if !m.avail(i, id, now) {
				return 0, "capacity"
			}
		}
		for i := e; i < e+need; i++ {
			m.bk[i].state = StateHeld
			m.bk[i].owner = id
		}
		m.exps[id] = [2]int{s, e + need}
		m.maxNow = now
		return s, ""
	}
}

func (m *naiveModel) release(id string, now int64) string {
	if id == "" || !validNaiveNow(now) {
		return "invalid"
	}
	rng, ok := m.exps[id]
	if !ok {
		return "missing"
	}
	if now < m.maxNow {
		return "rewind"
	}
	for i := rng[0]; i < rng[1]; i++ {
		m.bk[i].state = StateCooldown
		m.bk[i].owner = id
		m.bk[i].rel = now
		m.bk[i].k++
	}
	delete(m.exps, id)
	m.maxNow = now
	return ""
}

func (m *naiveModel) reshuffle(now int64) (int64, string) {
	if !validNaiveNow(now) {
		return m.g, "invalid"
	}
	if now < m.maxNow {
		return m.g, "rewind"
	}
	if m.g >= MaxGeneration {
		return m.g, "genmax"
	}
	for i := m.h; i < m.b; i++ {
		if m.bk[i].state == StateCooldown {
			m.bk[i].state = StateFree
			m.bk[i].owner = ""
			m.bk[i].rel = 0
		}
		m.bk[i].k = 0
	}
	m.g++
	m.maxNow = now
	return m.g, ""
}

func (m *naiveModel) lookup(h int64) string {
	if h < 0 || h > MaxHash {
		return "ERR invalid"
	}
	idx := (h + m.g*m.p) % int64(m.b)
	if idx < int64(m.h) {
		return fmt.Sprintf("ctl %d", idx)
	}
	x := m.bk[idx]
	switch x.state {
	case StateHeld:
		return fmt.Sprintf("held %d %s", idx, x.owner)
	case StateCooldown:
		return fmt.Sprintf("cool %d %s %d", idx, x.owner, x.rel+m.cd*m.mult(x.k))
	default:
		return fmt.Sprintf("free %d", idx)
	}
}

func errKey(err error) string {
	switch {
	case errIs(err, ErrInvalidArgument):
		return "invalid"
	case errIs(err, ErrExperimentExists):
		return "exists"
	case errIs(err, ErrExperimentNotFound):
		return "missing"
	case errIs(err, ErrClockRewound):
		return "rewind"
	case errIs(err, ErrGenerationExhausted):
		return "genmax"
	case errIs(err, ErrCapacity):
		return "capacity"
	case err == nil:
		return ""
	default:
		return "OTHER:" + err.Error()
	}
}

func lookupKey(r LookupResult, err error) string {
	if err != nil {
		return "ERR " + errKey(err)
	}
	if r.Control {
		return fmt.Sprintf("ctl %d", r.Bucket)
	}
	if r.Found {
		return fmt.Sprintf("held %d %s", r.Bucket, r.Experiment)
	}
	if r.Cooldown {
		return fmt.Sprintf("cool %d %s %d", r.Bucket, r.PrevOwner, r.AvailableAt)
	}
	return fmt.Sprintf("free %d", r.Bucket)
}

type op struct {
	kind string
	id   string
	n    int
	now  int64
	h    int64
}

func genOps(rng *rand.Rand, b, h, opsN int) []op {
	out := make([]op, 0, opsN)
	var clock int64
	nextID := 0
	for i := 0; i < opsN; i++ {
		roll := rng.Intn(100)
		var now int64
		if rng.Intn(8) == 0 && clock > 0 {
			now = rng.Int63n(clock + 1) // 故意回退
		} else {
			now = clock + int64(rng.Intn(6))
			if now > MaxNow {
				now = MaxNow
			}
			clock = now
		}
		switch {
		case roll < 28:
			id := fmt.Sprintf("e%d", nextID)
			nextID++
			n := 1 + rng.Intn(b-h)
			if rng.Intn(15) == 0 {
				n = 1 + rng.Intn(b+2)
			}
			out = append(out, op{kind: "claim", id: id, n: n, now: now})
		case roll < 52:
			id := fmt.Sprintf("e%d", rng.Intn(nextID+2))
			n2 := 1 + rng.Intn(b-h)
			if rng.Intn(15) == 0 {
				n2 = rng.Intn(b + 3)
			}
			out = append(out, op{kind: "resize", id: id, n: n2, now: now})
		case roll < 68:
			id := fmt.Sprintf("e%d", rng.Intn(nextID+2))
			out = append(out, op{kind: "release", id: id, now: now})
		case roll < 78:
			out = append(out, op{kind: "reshuffle", now: now})
		default:
			hv := rng.Int63n(int64(1) << 62)
			if rng.Intn(20) == 0 {
				hv = int64(1)<<62 + int64(rng.Intn(5))
			}
			out = append(out, op{kind: "lookup", h: hv})
		}
	}
	return out
}

func diffModel(o *Orchestrator, m *naiveModel) string {
	for i := o.hold; i < o.bucketsN; i++ {
		a, c := o.buckets[i], m.bk[i]
		if a.state != c.state || a.owner != c.owner || a.rel != c.rel || a.k != c.k {
			return fmt.Sprintf("bucket %d got(state=%d owner=%q rel=%d k=%d) naive(state=%d owner=%q rel=%d k=%d)",
				i, a.state, a.owner, a.rel, a.k, c.state, c.owner, c.rel, c.k)
		}
	}
	for id, r := range o.exps {
		cr, ok := m.exps[id]
		if !ok || cr[0] != r.start || cr[1] != r.end {
			return fmt.Sprintf("exp %s got [%d,%d) naive %v", id, r.start, r.end, cr)
		}
	}
	if len(o.exps) != len(m.exps) {
		return "experiment count differs"
	}
	if o.g != m.g || o.maxNow != m.maxNow {
		return fmt.Sprintf("g/maxNow got(%d,%d) naive(%d,%d)", o.g, o.maxNow, m.g, m.maxNow)
	}
	un := Usage{}
	for i := m.h; i < m.b; i++ {
		switch m.bk[i].state {
		case StateFree:
			un.Free++
		case StateHeld:
			un.Held++
		case StateCooldown:
			un.Cooldown++
		}
	}
	if uf := o.Usage(); uf != un {
		return fmt.Sprintf("usage got %+v naive %+v", uf, un)
	}
	return ""
}

// TestRandomAgainstNaive：2000 组随机序列逐步对照朴素模拟；日志含输入/输出/判定。
func TestRandomAgainstNaive(t *testing.T) {
	const groups, opsN = 2000, 120
	hist := map[string]int{}
	for gi := 0; gi < groups; gi++ {
		seed := int64(20261003 + gi)
		rng := rand.New(rand.NewSource(seed))
		b := 2 + rng.Intn(30)
		h := rng.Intn(b)
		cd := int64(rng.Intn(8))
		p := int64(1 + rng.Intn(7))
		ops := genOps(rand.New(rand.NewSource(seed^0x51ed)), b, h, opsN)

		o := mustNew(t, b, h, cd, p)
		m := naiveNew(b, h, cd, p)
		var logb strings.Builder
		fmt.Fprintf(&logb, "seed=%d B=%d H=%d Cd=%d P=%d ops=%d\n", seed, b, h, cd, p, len(ops))
		scanOK := true
		for step, x := range ops {
			var gotRet, naiveRet int
			var gotOut, naiveOut string
			var gotG, naiveG int64
			var e error
			beforeScan := o.lastScanExamined
			switch x.kind {
			case "claim":
				gotRet, e = o.Claim(x.id, x.n, x.now)
				gotOut = errKey(e)
				naiveRet, naiveOut = m.claim(x.id, x.n, x.now)
				if o.lastScanExamined > b-h {
					scanOK = false
				}
			case "resize":
				gotRet, e = o.Resize(x.id, x.n, x.now)
				gotOut = errKey(e)
				naiveRet, naiveOut = m.resize(x.id, x.n, x.now)
				if o.lastScanExamined > b-h {
					scanOK = false
				}
			case "release":
				gotOut = errKey(o.Release(x.id, x.now))
				naiveOut = m.release(x.id, x.now)
			case "reshuffle":
				gotG, e = o.Reshuffle(x.now)
				gotOut = errKey(e)
				naiveG, naiveOut = m.reshuffle(x.now)
			case "lookup":
				r, e := o.Lookup(x.h)
				gotOut = lookupKey(r, e)
				naiveOut = m.lookup(x.h)
				if o.lastScanExamined != beforeScan {
					scanOK = false
				}
			}
			bucket := naiveOut
			if x.kind == "lookup" {
				switch {
				case naiveOut == "ERR invalid":
					bucket = "invalid"
				case strings.HasPrefix(naiveOut, "ctl "):
					bucket = "control"
				case strings.HasPrefix(naiveOut, "held "):
					bucket = "held"
				case strings.HasPrefix(naiveOut, "cool "):
					bucket = "cooldown"
				default:
					bucket = "free"
				}
			}
			hist[x.kind+"/"+bucket]++
			fmt.Fprintf(&logb, "[%03d] %-9s id=%-6q n=%-3d now=%-3d h=%-4d => got(ret=%d,g=%d,%q) naive(ret=%d,g=%d,%q)\n",
				step, x.kind, x.id, x.n, x.now, x.h, gotRet, gotG, gotOut, naiveRet, naiveG, naiveOut)
			if gotOut != naiveOut {
				t.Fatalf("seed=%d step=%d %+v output mismatch:\n%s", seed, step, x, logb.String())
			}
			// 仅成功操作比较返回值（拒绝时返回值无定义）。
			if gotOut == "" {
				if (x.kind == "claim" || x.kind == "resize") && gotRet != naiveRet {
					t.Fatalf("seed=%d step=%d return mismatch: got=%d naive=%d\n%s", seed, step, gotRet, naiveRet, logb.String())
				}
				if x.kind == "reshuffle" && gotG != naiveG {
					t.Fatalf("seed=%d step=%d generation mismatch: got=%d naive=%d\n%s", seed, step, gotG, naiveG, logb.String())
				}
			}
			if d := diffModel(o, m); d != "" {
				t.Fatalf("seed=%d step=%d state mismatch: %s\n%s", seed, step, d, logb.String())
			}
			if !scanOK {
				t.Fatalf("seed=%d step=%d scan examined more than B buckets; Lookup must not scan", seed, step)
			}
		}
		if gi < 15 {
			t.Logf("group %d:\n%s", gi, logb.String())
		}
		t.Logf("group %4d seed=%d B=%d H=%d Cd=%d P=%d MATCH (%d ops)", gi, seed, b, h, cd, p, len(ops))
	}
	t.Logf("naive differential over %d groups: outcome histogram: %v", groups, hist)
}
