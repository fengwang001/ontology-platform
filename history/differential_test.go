package history_test

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/history"
	"ontology/lease"
	"ontology/recovery"
)

// 朴素模型：严格按题面规则独立重写。
type modelOp struct {
	seq int
	id  string
	del bool
}

type modelLease struct{ r, lastRenew int }

type model struct {
	e, lmax        int
	ops            []modelOp
	purged         map[int]bool
	latest         map[string]int
	live           map[string]int
	leases         map[string]modelLease
	h, gcp, maxSeq int
	now            int
	seenNow        bool
}

func newModel(e, lmax int) *model {
	return &model{
		e: e, lmax: lmax,
		purged: map[int]bool{},
		latest: map[string]int{},
		live:   map[string]int{},
		leases: map[string]modelLease{},
		h:      1,
	}
}

func (m *model) validNow(now int) bool { return now >= 0 && now <= 1_000_000_000_000 }

func (m *model) checkClock(now int) error {
	if !m.validNow(now) {
		return history.ErrInvalidArgument
	}
	if m.seenNow && now < m.now {
		return history.ErrClockBacktrack
	}
	return nil
}

func (m *model) index(now int, id string) (int, error) {
	if len(id) < 1 || len(id) > 256 {
		return 0, history.ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return 0, err
	}
	m.maxSeq++
	m.ops = append(m.ops, modelOp{seq: m.maxSeq, id: id})
	m.latest[id] = m.maxSeq
	m.live[id] = m.maxSeq
	m.now, m.seenNow = now, true
	return m.maxSeq, nil
}

func (m *model) del(now int, id string) (int, error) {
	if len(id) < 1 || len(id) > 256 {
		return 0, history.ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return 0, err
	}
	if _, ok := m.live[id]; !ok {
		return 0, history.ErrDocNotFound
	}
	m.maxSeq++
	m.ops = append(m.ops, modelOp{seq: m.maxSeq, id: id, del: true})
	m.latest[id] = m.maxSeq
	delete(m.live, id)
	m.now, m.seenNow = now, true
	return m.maxSeq, nil
}

func (m *model) addLease(now int, name string, r int) error {
	if !m.validNow(now) || name == "" || r < 1 || r > m.maxSeq+1 {
		return lease.ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if _, ok := m.leases[name]; ok {
		return lease.ErrLeaseExists
	}
	if r < m.h {
		return lease.ErrHistoryMissing
	}
	if len(m.leases) >= m.lmax {
		return lease.ErrLeaseLimit
	}
	m.leases[name] = modelLease{r: r, lastRenew: now}
	m.now, m.seenNow = now, true
	return nil
}

func (m *model) renewLease(now int, name string, r int) error {
	if !m.validNow(now) || name == "" {
		return lease.ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	e, ok := m.leases[name]
	if !ok {
		return lease.ErrLeaseNotFound
	}
	if r < e.r {
		return lease.ErrLeaseBacktrack
	}
	e.r, e.lastRenew = r, now
	m.leases[name] = e
	m.now, m.seenNow = now, true
	return nil
}

func (m *model) removeLease(now int, name string) error {
	if !m.validNow(now) || name == "" {
		return lease.ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if _, ok := m.leases[name]; !ok {
		return lease.ErrLeaseNotFound
	}
	delete(m.leases, name)
	m.now, m.seenNow = now, true
	return nil
}

func (m *model) setGCP(now, g int) error {
	if !m.validNow(now) || g < 0 || g > m.maxSeq {
		return lease.ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if g < m.gcp {
		return lease.ErrGCPBacktrack
	}
	m.gcp = g
	m.now, m.seenNow = now, true
	return nil
}

type modelMerge struct {
	purged  int
	removed []string
}

func (m *model) merge(now int) (modelMerge, error) {
	if err := m.checkClock(now); err != nil {
		return modelMerge{}, err
	}
	var removed []string
	for name, e := range m.leases {
		if now-e.lastRenew > m.e {
			removed = append(removed, name)
			delete(m.leases, name)
		}
	}
	sort.Strings(removed)
	floor := m.gcp + 1
	for _, e := range m.leases {
		if e.r < floor {
			floor = e.r
		}
	}
	purged := 0
	for seq := m.h; seq < floor; seq++ {
		op := m.ops[seq-1]
		if op.del || m.latest[op.id] > seq {
			m.purged[seq] = true
			purged++
		}
	}
	m.h = floor
	m.now, m.seenNow = now, true
	return modelMerge{purged: purged, removed: removed}, nil
}

type recOp struct {
	kind     int
	now      int
	id, name string
	r, g     int
}

func replay(recs []recOp, e, lmax int) *history.Primary {
	p := history.NewPrimary(e, lmax)
	for _, o := range recs {
		switch o.kind {
		case 0:
			p.Index(o.now, o.id)
		case 1:
			p.Delete(o.now, o.id)
		case 2:
			p.AddLease(o.now, o.name, o.r)
		case 3:
			p.RenewLease(o.now, o.name, o.r)
		case 4:
			p.RemoveLease(o.now, o.name)
		case 5:
			p.SetGlobalCheckpoint(o.now, o.g)
		case 6:
			p.Merge(o.now)
		}
	}
	return p
}

func sameErr(a, b error) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		for _, sentinel := range allSentinels {
			if isErr(a, sentinel) != isErr(b, sentinel) {
				return false
			}
		}
		return true
	}
}

func isErr(err, target error) bool {
	type iser interface{ Is(error) bool }
	if x, ok := err.(iser); ok {
		return x.Is(target)
	}
	return err == target
}

var allSentinels = []error{
	history.ErrInvalidArgument, history.ErrClockBacktrack, history.ErrDocNotFound,
	lease.ErrInvalidArgument, lease.ErrClockBacktrack, lease.ErrLeaseExists,
	lease.ErrLeaseNotFound, lease.ErrLeaseBacktrack, lease.ErrHistoryMissing,
	lease.ErrLeaseLimit, lease.ErrGCPBacktrack,
}

func assertSameState(t *testing.T, p *history.Primary, m *model, step string) {
	t.Helper()
	if p.MaxSeq() != m.maxSeq || p.H() != m.h || p.GCP() != m.gcp {
		t.Fatalf("%s: state maxSeq p=%d m=%d H p=%d m=%d gcp p=%d m=%d",
			step, p.MaxSeq(), m.maxSeq, p.H(), m.h, p.GCP(), m.gcp)
	}
	got := p.Leases()
	if len(got) != len(m.leases) {
		t.Fatalf("%s: lease count p=%v m=%v", step, got, m.leases)
	}
	for _, lv := range got {
		e, ok := m.leases[lv.Name]
		if !ok || e.r != lv.R || e.lastRenew != lv.LastRenew {
			t.Fatalf("%s: lease %s p=(%d,%d) m=(%d,%d)", step, lv.Name, lv.R, lv.LastRenew, e.r, e.lastRenew)
		}
	}
}

func mapsEq(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// assertPlans：对每个 c 应用 Plan(c) 后，副本存活集必须与主一致；并校验 Ops 连续无洞。
func assertPlans(t *testing.T, p *history.Primary, m *model, step string) {
	t.Helper()
	wantLive := map[string]int{}
	for id, seq := range m.live {
		wantLive[id] = seq
	}
	for c := 0; c <= m.maxSeq; c++ {
		pl, err := p.Plan(c)
		if err != nil {
			t.Fatalf("%s: Plan(%d) err=%v", step, c, err)
		}
		if c+1 >= m.h {
			if pl.Mode != recovery.OpsBased {
				t.Fatalf("%s: c=%d c+1=%d>=H=%d 应 OpsBased", step, c, c+1, m.h)
			}
			if len(pl.Ops) != m.maxSeq-c {
				t.Fatalf("%s: c=%d OpsBased 有洞或缺失: ops=%d want=%d", step, c, len(pl.Ops), m.maxSeq-c)
			}
			for i, op := range pl.Ops {
				if op.Seq != c+1+i {
					t.Fatalf("%s: c=%d 非连续: 第%d个 seq=%d want %d", step, c, i, op.Seq, c+1+i)
				}
			}
			// 副本在 c 的状态（用模型全量历史重放到 c），应用返回 ops。
			rep := map[string]int{}
			for _, op := range m.ops {
				if op.seq > c {
					break
				}
				if op.del {
					delete(rep, op.id)
				} else {
					rep[op.id] = op.seq
				}
			}
			for _, op := range pl.Ops {
				if op.Kind == recovery.KindDelete {
					delete(rep, op.ID)
				} else {
					rep[op.ID] = op.Seq
				}
			}
			if !mapsEq(rep, wantLive) {
				t.Fatalf("%s: c=%d OpsBased 应用后 %v != 主 %v", step, c, rep, wantLive)
			}
		} else {
			if pl.Mode != recovery.FileBased {
				t.Fatalf("%s: c=%d c+1=%d<H=%d 应 FileBased", step, c, c+1, m.h)
			}
			if pl.MaxSeq != m.maxSeq || len(pl.Docs) != len(wantLive) {
				t.Fatalf("%s: c=%d FileBased maxSeq/docs 不符", step, c)
			}
			for i, d := range pl.Docs {
				if i > 0 && pl.Docs[i-1].ID >= d.ID {
					t.Fatalf("%s: FileBased docs 未按 id 字节序", step)
				}
				if wantLive[d.ID] != d.Seq {
					t.Fatalf("%s: c=%d doc %s p=%d m=%d", step, c, d.ID, d.Seq, wantLive[d.ID])
				}
			}
			got := map[string]int{}
			for _, d := range pl.Docs {
				got[d.ID] = d.Seq
			}
			if !mapsEq(got, wantLive) {
				t.Fatalf("%s: c=%d FileBased docs %v != 主 %v", step, c, got, wantLive)
			}
		}
	}
}

func TestRandomDifferential(t *testing.T) {
	const sequences = 1500
	const steps = 50
	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	names := []string{"L1", "L2", "L3", "L4"}
	for seed := int64(0); seed < sequences; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			e := 1 + rng.Intn(10)
			lmax := 1 + rng.Intn(3)
			p := history.NewPrimary(e, lmax)
			m := newModel(e, lmax)
			var recs []recOp
			now := 0
			for step := 0; step < steps; step++ {
				// 时钟：多数非递减，少数回退以触发"时钟回退/被拒不剔除"。
				if step > 0 && rng.Intn(7) == 0 {
					now -= 1 + rng.Intn(3)
					if now < 0 {
						now = 0
					}
				} else {
					now += rng.Intn(4)
				}
				id := ids[rng.Intn(len(ids))]
				name := names[rng.Intn(len(names))]
				if rng.Intn(20) == 0 {
					name = ""
				}
				tag := fmt.Sprintf("seed=%d step=%d now=%d state(maxSeq=%d,H=%d,gcp=%d)",
					seed, step, now, m.maxSeq, m.h, m.gcp)
				var rec recOp
				var in, out string
				switch kind := rng.Intn(10); {
				case kind < 3:
					if rng.Intn(20) == 0 {
						id = ""
					}
					s1, e1 := p.Index(now, id)
					s2, e2 := m.index(now, id)
					rec = recOp{kind: 0, now: now, id: id}
					in = fmt.Sprintf("Index(now=%d,id=%q)", now, id)
					out = fmt.Sprintf("p=(%d,%v) m=(%d,%v)", s1, e1, s2, e2)
					if s1 != s2 || !sameErr(e1, e2) {
						t.Fatalf("%s %s 不一致: %s", tag, in, out)
					}
				case kind < 5:
					s1, e1 := p.Delete(now, id)
					s2, e2 := m.del(now, id)
					rec = recOp{kind: 1, now: now, id: id}
					in = fmt.Sprintf("Delete(now=%d,id=%q)", now, id)
					out = fmt.Sprintf("p=(%d,%v) m=(%d,%v)", s1, e1, s2, e2)
					if s1 != s2 || !sameErr(e1, e2) {
						t.Fatalf("%s %s 不一致: %s", tag, in, out)
					}
				case kind == 5:
					r := 1 + rng.Intn(m.maxSeq+2)
					if rng.Intn(10) == 0 {
						r = 0
					}
					e1 := p.AddLease(now, name, r)
					e2 := m.addLease(now, name, r)
					rec = recOp{kind: 2, now: now, name: name, r: r}
					in = fmt.Sprintf("AddLease(now=%d,name=%q,r=%d)", now, name, r)
					out = fmt.Sprintf("p=%v m=%v", e1, e2)
					if !sameErr(e1, e2) {
						t.Fatalf("%s %s 不一致: %s", tag, in, out)
					}
				case kind == 6:
					r := 1 + rng.Intn(m.maxSeq+2)
					e1 := p.RenewLease(now, name, r)
					e2 := m.renewLease(now, name, r)
					rec = recOp{kind: 3, now: now, name: name, r: r}
					in = fmt.Sprintf("RenewLease(now=%d,name=%q,r=%d)", now, name, r)
					out = fmt.Sprintf("p=%v m=%v", e1, e2)
					if !sameErr(e1, e2) {
						t.Fatalf("%s %s 不一致: %s", tag, in, out)
					}
				case kind == 7:
					e1 := p.RemoveLease(now, name)
					e2 := m.removeLease(now, name)
					rec = recOp{kind: 4, now: now, name: name}
					in = fmt.Sprintf("RemoveLease(now=%d,name=%q)", now, name)
					out = fmt.Sprintf("p=%v m=%v", e1, e2)
					if !sameErr(e1, e2) {
						t.Fatalf("%s %s 不一致: %s", tag, in, out)
					}
				case kind == 8:
					g := rng.Intn(m.maxSeq + 2)
					e1 := p.SetGlobalCheckpoint(now, g)
					e2 := m.setGCP(now, g)
					rec = recOp{kind: 5, now: now, g: g}
					in = fmt.Sprintf("SetGlobalCheckpoint(now=%d,g=%d)", now, g)
					out = fmt.Sprintf("p=%v m=%v", e1, e2)
					if !sameErr(e1, e2) {
						t.Fatalf("%s %s 不一致: %s", tag, in, out)
					}
				default:
					r1, e1 := p.Merge(now)
					r2, e2 := m.merge(now)
					rec = recOp{kind: 6, now: now}
					in = fmt.Sprintf("Merge(now=%d)", now)
					out = fmt.Sprintf("p=(purged=%d,removed=%v,%v) m=(purged=%d,removed=%v,%v)",
						r1.Purged, r1.Removed, e1, r2.purged, r2.removed, e2)
					if r1.Purged != r2.purged ||
						(len(r1.Removed) != len(r2.removed)) || !sameErr(e1, e2) {
						t.Fatalf("%s %s 不一致: %s", tag, in, out)
					}
					for i := range r2.removed {
						if r1.Removed[i] != r2.removed[i] {
							t.Fatalf("%s %s 剔除名序不一致: %s", tag, in, out)
						}
					}
				}
				recs = append(recs, rec)
				assertSameState(t, p, m, tag+" "+in)
				assertPlans(t, p, m, tag+" "+in)
				basis := "accepted"
				if out != "" && len(out) >= 4 && (out[0:2] == "p=" || containsErr(out)) {
					if containsErr(out) {
						basis = "rejected(状态不变, 不剔除)"
					}
				}
				t.Logf("%s | 输入 %s | 输出 %s | 判定 %s", tag, in, out, basis)
			}
			// 相同序列重放结果相同（确定性）。
			p2 := replay(recs, e, lmax)
			if p2.MaxSeq() != p.MaxSeq() || p2.H() != p.H() || p2.GCP() != p.GCP() {
				t.Fatalf("seed=%d 重放状态不一致", seed)
			}
			d1, _ := p.Plan(0)
			d2, _ := p2.Plan(0)
			if len(d1.Docs) != len(d2.Docs) {
				t.Fatalf("seed=%d 重放 FileBased docs 不一致", seed)
			}
			for i := range d1.Docs {
				if d1.Docs[i] != d2.Docs[i] {
					t.Fatalf("seed=%d 重放 docs[%d] %v != %v", seed, i, d1.Docs[i], d2.Docs[i])
				}
			}
			assertSameState(t, p2, m, fmt.Sprintf("seed=%d replay", seed))
		})
	}
}

func containsErr(s string) bool {
	for _, target := range []string{
		"invalid argument", "clock backtrack", "document not found",
		"lease already exists", "lease not found", "r backtrack",
		"history not available", "exceeds Lmax", "checkpoint backtrack",
	} {
		if indexOf(s, target) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
