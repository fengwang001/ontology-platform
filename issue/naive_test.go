package issue

import (
	"math/rand"
	"sort"
	"testing"

	"ontology/bloodstock"
	"ontology/typing"
)

type naiveBag struct {
	id         string
	abo        bloodstock.ABO
	rh         bloodstock.Rh
	exp        int64
	status     bloodstock.Status
	patient    string
	reservedAt int64
	issuedAt   int64
}

type naiveTypeRec struct {
	abo bloodstock.ABO
	rh  bloodstock.Rh
}

type naiveSim struct {
	m, h      int64
	maxNow    int64
	bags      map[string]*naiveBag
	samples   map[string]bool
	recs      map[string][]naiveTypeRec
	resolved  map[string]naiveTypeRec
	forceDisp map[string]bool
	roles     map[string]map[Role]bool
}

func newNaive(M, H int64) *naiveSim {
	return &naiveSim{
		m: M, h: H,
		bags:      map[string]*naiveBag{},
		samples:   map[string]bool{},
		recs:      map[string][]naiveTypeRec{},
		resolved:  map[string]naiveTypeRec{},
		forceDisp: map[string]bool{},
		roles:     map[string]map[Role]bool{},
	}
}

func (s *naiveSim) kind(p string) typing.Kind {
	if s.forceDisp[p] {
		return typing.Disputed
	}
	if _, ok := s.resolved[p]; ok {
		return typing.Confirmed
	}
	rs := s.recs[p]
	switch {
	case len(rs) == 0:
		return typing.Unknown
	case len(rs) == 1:
		return typing.Single
	}
	first := rs[0]
	for _, r := range rs[1:] {
		if r != first {
			return typing.Disputed
		}
	}
	return typing.Confirmed
}

func (s *naiveSim) current(p string) (bloodstock.ABO, bloodstock.Rh) {
	if r, ok := s.resolved[p]; ok {
		return r.abo, r.rh
	}
	return s.recs[p][0].abo, s.recs[p][0].rh
}

func (s *naiveSim) order(p string) []int {
	k := s.kind(p)
	if k == typing.Unknown || k == typing.Disputed {
		return []int{bloodstock.Slot(bloodstock.O, bloodstock.Negative)}
	}
	abo, rh := s.current(p)
	if k == typing.Single {
		if rh == bloodstock.Negative {
			return []int{bloodstock.Slot(bloodstock.O, bloodstock.Negative)}
		}
		return []int{bloodstock.Slot(bloodstock.O, bloodstock.Positive), bloodstock.Slot(bloodstock.O, bloodstock.Negative)}
	}
	var abos []bloodstock.ABO
	switch abo {
	case bloodstock.A:
		abos = []bloodstock.ABO{bloodstock.A, bloodstock.O}
	case bloodstock.B:
		abos = []bloodstock.ABO{bloodstock.B, bloodstock.O}
	case bloodstock.AB:
		abos = []bloodstock.ABO{bloodstock.AB, bloodstock.A, bloodstock.B, bloodstock.O}
	case bloodstock.O:
		abos = []bloodstock.ABO{bloodstock.O}
	}
	var out []int
	if rh == bloodstock.Negative {
		for _, a := range abos {
			out = append(out, bloodstock.Slot(a, bloodstock.Negative))
		}
		return out
	}
	for _, a := range abos {
		out = append(out, bloodstock.Slot(a, bloodstock.Positive), bloodstock.Slot(a, bloodstock.Negative))
	}
	return out
}

// land 朴素落地：每次全量收集事件、全量排序后处理。
func (s *naiveSim) land(now int64) {
	type ev struct {
		at   int64
		kind int
		bag  string
	}
	var evs []ev
	for _, b := range s.bags {
		if b.status == bloodstock.Available || b.status == bloodstock.Reserved {
			evs = append(evs, ev{b.exp, 1, b.id})
		}
		if b.status == bloodstock.Reserved {
			evs = append(evs, ev{b.reservedAt + s.h, 0, b.id})
		}
	}
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].at != evs[j].at {
			return evs[i].at < evs[j].at
		}
		if evs[i].kind != evs[j].kind {
			return evs[i].kind > evs[j].kind
		}
		return evs[i].bag < evs[j].bag
	})
	for _, e := range evs {
		if e.at > now {
			continue
		}
		b := s.bags[e.bag]
		switch e.kind {
		case 0:
			if b.status == bloodstock.Reserved {
				if now >= b.exp {
					b.status = bloodstock.Discarded
					b.patient = ""
					b.reservedAt = 0
				} else {
					b.status = bloodstock.Available
					b.patient = ""
					b.reservedAt = 0
				}
			}
		case 1:
			if b.status == bloodstock.Available || b.status == bloodstock.Reserved {
				b.status = bloodstock.Discarded
				b.patient = ""
				b.reservedAt = 0
			}
		}
	}
}

func (s *naiveSim) hasRole(u string, r Role) bool {
	return s.roles[u] != nil && s.roles[u][r]
}

func (s *naiveSim) grant(u string, r Role) {
	if s.roles[u] == nil {
		s.roles[u] = map[Role]bool{}
	}
	s.roles[u][r] = true
}

type op struct {
	kind int
	now  int64
	args []string
	n    int
	abo  bloodstock.ABO
	rh   bloodstock.Rh
}

const (
	opAdd = iota
	opType
	opResolve
	opCross
	opIssue
	opReturn
	opDiscard
)

func (s *naiveSim) run(o op) ([]string, error) {
	switch o.kind {
	case opAdd:
		id := o.args[0]
		exp := int64(o.n)
		if id == "" || o.abo < 0 || o.abo > 3 || (o.rh != bloodstock.Positive && o.rh != bloodstock.Negative) || exp <= o.now {
			return nil, ErrInvalid
		}
		if o.now < s.maxNow {
			return nil, ErrClock
		}
		if _, ok := s.bags[id]; ok {
			return nil, ErrBagDup
		}
		s.land(o.now)
		s.bags[id] = &naiveBag{id: id, abo: o.abo, rh: o.rh, exp: exp, status: bloodstock.Available}
		s.maxNow = o.now
		return nil, nil
	case opType:
		p, sample := o.args[1], o.args[2]
		if p == "" || sample == "" || o.abo < 0 || o.abo > 3 {
			return nil, ErrInvalid
		}
		if o.now < s.maxNow {
			return nil, ErrClock
		}
		if s.samples[sample] {
			return nil, ErrSampleDup
		}
		s.land(o.now)
		before := s.kind(p)
		// 与「此前结果」矛盾：已存疑时以第一条记录为基准（与 Registry 一致）。
		var baseline *naiveTypeRec
		if rs := s.recs[p]; len(rs) > 0 {
			b := rs[0]
			baseline = &b
		} else if rv, ok := s.resolved[p]; ok {
			b := rv
			baseline = &b
		}
		contradNow := baseline != nil && (baseline.abo != o.abo || baseline.rh != o.rh)
		rv, wasResolved := s.resolved[p]
		resolvedContradiction := wasResolved && (rv.abo != o.abo || rv.rh != o.rh)
		delete(s.resolved, p)
		if resolvedContradiction {
			s.forceDisp[p] = true
		}
		s.recs[p] = append(s.recs[p], naiveTypeRec{o.abo, o.rh})
		s.samples[sample] = true
		if contradNow || resolvedContradiction || (before != typing.Disputed && s.kind(p) == typing.Disputed) {
			for _, b := range s.bags {
				if b.status == bloodstock.Reserved && b.patient == p {
					b.status = bloodstock.Available
					b.patient = ""
					b.reservedAt = 0
				}
			}
		}
		s.maxNow = o.now
		return nil, nil
	case opResolve:
		sup, p := o.args[0], o.args[1]
		if p == "" || sup == "" || o.abo < 0 || o.abo > 3 {
			return nil, ErrInvalid
		}
		if o.now < s.maxNow {
			return nil, ErrClock
		}
		if !s.hasRole(sup, RoleSupervisor) {
			return nil, ErrForbidden
		}
		s.land(o.now)
		s.resolved[p] = naiveTypeRec{o.abo, o.rh}
		// 与实现一致：裁定重置鉴定历史。
		delete(s.recs, p)
		delete(s.forceDisp, p)
		s.maxNow = o.now
		return nil, nil
	case opCross:
		tech, p := o.args[0], o.args[1]
		if tech == "" || p == "" || o.n < 1 || o.n > 20 {
			return nil, ErrInvalid
		}
		if o.now < s.maxNow {
			return nil, ErrClock
		}
		if !s.hasRole(tech, RoleTech) {
			return nil, ErrForbidden
		}
		s.land(o.now)
		rank := map[int]int{}
		for i, sl := range s.order(p) {
			rank[sl] = i
		}
		var cands []*naiveBag
		for _, b := range s.bags {
			if b.status != bloodstock.Available || b.exp <= o.now+s.m {
				continue
			}
			if _, ok := rank[bloodstock.Slot(b.abo, b.rh)]; !ok {
				continue
			}
			cands = append(cands, b)
		}
		sort.Slice(cands, func(i, j int) bool {
			ri := rank[bloodstock.Slot(cands[i].abo, cands[i].rh)]
			rj := rank[bloodstock.Slot(cands[j].abo, cands[j].rh)]
			if ri != rj {
				return ri < rj
			}
			if cands[i].exp != cands[j].exp {
				return cands[i].exp < cands[j].exp
			}
			return cands[i].id < cands[j].id
		})
		s.maxNow = o.now
		if len(cands) < o.n {
			return nil, ErrStock
		}
		var picked []string
		for _, b := range cands[:o.n] {
			b.status = bloodstock.Reserved
			b.patient = p
			b.reservedAt = o.now
			picked = append(picked, b.id)
		}
		return picked, nil
	case opIssue:
		n1, n2, p, bag := o.args[0], o.args[1], o.args[2], o.args[3]
		if n1 == "" || n2 == "" || n1 == n2 || p == "" || bag == "" {
			return nil, ErrInvalid
		}
		if o.now < s.maxNow {
			return nil, ErrClock
		}
		b, ok := s.bags[bag]
		if !ok {
			return nil, ErrNotFound
		}
		if !s.hasRole(n1, RoleIssue) || !s.hasRole(n2, RoleIssue) {
			return nil, ErrForbidden
		}
		s.land(o.now)
		if b.status != bloodstock.Reserved || b.patient != p {
			s.maxNow = o.now
			return nil, ErrState
		}
		b.status = bloodstock.Issued
		b.patient = ""
		b.reservedAt = 0
		b.issuedAt = o.now
		s.maxNow = o.now
		return nil, nil
	case opReturn:
		u, bag := o.args[0], o.args[1]
		if u == "" || bag == "" {
			return nil, ErrInvalid
		}
		if o.now < s.maxNow {
			return nil, ErrClock
		}
		b, ok := s.bags[bag]
		if !ok {
			return nil, ErrNotFound
		}
		if !s.hasRole(u, RoleIssue) {
			return nil, ErrForbidden
		}
		s.land(o.now)
		s.maxNow = o.now
		if b.status != bloodstock.Issued {
			return nil, ErrState
		}
		if o.now-b.issuedAt > 30 {
			return nil, ErrReturnLate
		}
		b.status = bloodstock.Available
		b.issuedAt = 0
		if o.now >= b.exp {
			b.status = bloodstock.Discarded
		}
		return nil, nil
	case opDiscard:
		u, bag := o.args[0], o.args[1]
		if u == "" || bag == "" {
			return nil, ErrInvalid
		}
		if o.now < s.maxNow {
			return nil, ErrClock
		}
		b, ok := s.bags[bag]
		if !ok {
			return nil, ErrNotFound
		}
		s.land(o.now)
		s.maxNow = o.now
		if b.status == bloodstock.Discarded {
			return nil, ErrState
		}
		b.status = bloodstock.Discarded
		b.patient = ""
		b.reservedAt = 0
		b.issuedAt = 0
		return nil, nil
	}
	return nil, nil
}

// snapshot 比较双方全部血袋状态与患者鉴定状态。
func (s *naiveSim) snapshot(m *Manager) (string, bool) {
	for id, nb := range s.bags {
		st, ok := m.BagStatus(id)
		if !ok {
			return "bag missing in real: " + id, false
		}
		if st != nb.status {
			return "status mismatch " + id, false
		}
		if st == bloodstock.Reserved && m.BagPatient(id) != nb.patient {
			return "patient mismatch " + id, false
		}
	}
	for p := range s.recs {
		if m.PatientKind(p) != s.kind(p) {
			return "kind mismatch " + p, false
		}
	}
	return "", true
}

func runReal(m *Manager, o op) ([]string, error) {
	switch o.kind {
	case opAdd:
		return nil, m.AddBag(o.now, o.args[0], o.abo, o.rh, int64(o.n))
	case opType:
		return nil, m.Type(o.now, o.args[0], o.args[1], o.args[2], o.abo, o.rh)
	case opResolve:
		return nil, m.Resolve(o.now, o.args[0], o.args[1], o.abo, o.rh)
	case opCross:
		bags, _, err := m.Crossmatch(o.now, o.args[0], o.args[1], o.n)
		return bags, err
	case opIssue:
		return nil, m.Issue(o.now, o.args[0], o.args[1], o.args[2], o.args[3])
	case opReturn:
		return nil, m.Return(o.now, o.args[0], o.args[1])
	case opDiscard:
		return nil, m.Discard(o.now, o.args[0], o.args[1])
	}
	return nil, nil
}

// genOps 生成一条随机操作序列；now 非降（带少量回退尝试以覆盖拒绝路径）。
func genOps(rng *rand.Rand, seed int64) []op {
	var ops []op
	now := int64(0)
	users := []string{"tech", "issue1", "issue2", "sup", "nobody"}
	patients := []string{"P1", "P2", "P3", "P4"}
	var bagIDs []string
	sampleSeq := int64(0)
	bagSeq := int64(0)
	steps := 40 + rng.Intn(40)
	for i := 0; i < steps; i++ {
		roll := rng.Intn(100)
		switch {
		case roll < 25:
			id := "b" + itoa2(bagSeq)
			bagSeq++
			exp := now + int64(1+rng.Intn(6000))
			ops = append(ops, op{kind: opAdd, now: now, args: []string{id}, n: int(exp),
				abo: bloodstock.ABO(rng.Intn(4)), rh: bloodstock.Rh(rng.Intn(2))})
			bagIDs = append(bagIDs, id)
		case roll < 45:
			sampleSeq++
			ops = append(ops, op{kind: opType, now: now,
				args: []string{"lab", patients[rng.Intn(len(patients))], "s" + itoa2(seed) + "-" + itoa2(int64(sampleSeq))},
				abo:  bloodstock.ABO(rng.Intn(4)),
				rh:   bloodstock.Rh(rng.Intn(2))})
		case roll < 50:
			ops = append(ops, op{kind: opResolve, now: now,
				args: []string{pickUser(rng, users), patients[rng.Intn(len(patients))]},
				abo:  bloodstock.ABO(rng.Intn(4)),
				rh:   bloodstock.Rh(rng.Intn(2))})
		case roll < 72:
			ops = append(ops, op{kind: opCross, now: now,
				args: []string{pickUser(rng, users), patients[rng.Intn(len(patients))]},
				n:    1 + rng.Intn(6)})
		case roll < 84 && len(bagIDs) > 0:
			ops = append(ops, op{kind: opIssue, now: now,
				args: []string{pickUser(rng, users), pickUser(rng, users),
					patients[rng.Intn(len(patients))], bagIDs[rng.Intn(len(bagIDs))]}})
		case roll < 92 && len(bagIDs) > 0:
			ops = append(ops, op{kind: opReturn, now: now,
				args: []string{pickUser(rng, users), bagIDs[rng.Intn(len(bagIDs))]}})
		case len(bagIDs) > 0:
			ops = append(ops, op{kind: opDiscard, now: now,
				args: []string{pickUser(rng, users), bagIDs[rng.Intn(len(bagIDs))]}})
		default:
			ops = append(ops, op{kind: opDiscard, now: now,
				args: []string{"tech", "b0"}})
		}
		// 5% 概率尝试时钟回退（应被双方一致拒绝且不推进）。
		if rng.Intn(20) == 0 && now > 10 {
			now -= int64(rng.Intn(5) + 1)
		} else {
			now += int64(rng.Intn(800))
		}
	}
	return ops
}

func pickUser(rng *rand.Rand, users []string) string { return users[rng.Intn(len(users))] }

func itoa2(x int64) string {
	if x == 0 {
		return "0"
	}
	var b []byte
	for x > 0 {
		b = append([]byte{byte('0' + x%10)}, b...)
		x /= 10
	}
	return string(b)
}

func TestNaiveDifferential(t *testing.T) {
	const groups = 1500
	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(int64(g + 1)))
		M := int64(rng.Intn(1000))
		H := int64(rng.Intn(5000))
		ops := genOps(rng, int64(g))

		real := New(M, H)
		real.Grant("tech", RoleTech)
		real.Grant("issue1", RoleIssue)
		real.Grant("issue2", RoleIssue)
		real.Grant("sup", RoleSupervisor)
		// 打开日志：失败用例打印输入/输出/判定依据（-v 时可见）。
		real.SetLogger(func(format string, args ...any) {
			t.Logf("[g=%d] "+format, append([]any{g}, args...)...)
		})

		nav := newNaive(M, H)
		nav.grant("tech", RoleTech)
		nav.grant("issue1", RoleIssue)
		nav.grant("issue2", RoleIssue)
		nav.grant("sup", RoleSupervisor)

		for i, o := range ops {
			gotBags, gotErr := runReal(real, o)
			wantBags, wantErr := nav.run(o)
			if errCode(gotErr) != errCode(wantErr) {
				t.Fatalf("g=%d op=%d/%d %+v: err got=%v want=%v", g, i, len(ops), o, gotErr, wantErr)
			}
			if !eqBags(gotBags, wantBags) {
				t.Fatalf("g=%d op=%d %+v: bags got=%v want=%v", g, i, o, gotBags, wantBags)
			}
			if msg, same := nav.snapshot(real); !same {
				t.Fatalf("g=%d op=%d %+v 状态分歧: %s", g, i, o, msg)
			}
		}
	}
}

func eqBags(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func errCode(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

func TestExaminedBoundScales(t *testing.T) {
	for _, total := range []int{100, 10000} {
		t.Run(itoa2(int64(total)), func(t *testing.T) {
			s := bloodstock.NewStore(100)
			// 全部 O+，效期集中在一个窄窗口。
			for i := 0; i < total; i++ {
				s.Add("bag"+itoa2(int64(i)), bloodstock.O, bloodstock.Positive, 1000)
			}
			// now=950 -> deadline=1050，全部 exp<=deadline 被排除。
			_, examined, ok := s.Reserve(950, 100000,
				[]int{
					bloodstock.Slot(bloodstock.A, bloodstock.Positive),
					bloodstock.Slot(bloodstock.A, bloodstock.Negative),
					bloodstock.Slot(bloodstock.O, bloodstock.Positive),
				}, "P", 5)
			if ok {
				t.Fatal("应库存不足")
			}
			excluded := total
			if examined > 5+excluded+8 {
				t.Fatalf("total=%d examined=%d 超过 n+排除+8", total, examined)
			}
			t.Logf("total=%d examined=%d (上界 %d)", total, examined, 5+excluded+8)

			// 成功路径：新增 10 袋远效期，考察量仍只与 n/排除相关。
			for i := 0; i < 10; i++ {
				s.Add("fresh"+itoa2(int64(i)), bloodstock.O, bloodstock.Positive, 5000)
			}
			picked, examined2, ok := s.Reserve(950, 100000,
				[]int{bloodstock.Slot(bloodstock.O, bloodstock.Positive)}, "P", 3)
			if !ok || len(picked) != 3 {
				t.Fatalf("应预留3袋 got %v", picked)
			}
			if examined2 > 3+0+8 {
				t.Fatalf("成功路径 examined=%d 超过 n+8", examined2)
			}
			t.Logf("total=%d 成功路径 examined=%d", total+10, examined2)
		})
	}
}
