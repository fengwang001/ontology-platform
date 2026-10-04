package push

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/group"
)

// naiveSim 是朴素参考实现：每个被接受的批次后对全部设备重新求值并对齐。
// 它独立维护一份 group 状态，不调用被测代码的求值/对齐逻辑。
type naiveSim struct {
	gmax       int
	groups     map[group.Name]*naiveGroup
	devices    map[group.Name]map[group.Name]struct{}
	ack        map[group.Name]Config
	pd         map[group.Name]Config
	pid        map[group.Name]int
	taskStatus map[int]string
	nacks      map[int]int
	next       int
}

type naiveGroup struct {
	pr      int
	builtin bool
	policy  group.Policy
	members map[group.Name]struct{}
}

func newNaive(gmax int) *naiveSim {
	m := &naiveSim{
		gmax:       gmax,
		groups:     map[group.Name]*naiveGroup{},
		devices:    map[group.Name]map[group.Name]struct{}{},
		ack:        map[group.Name]Config{},
		pd:         map[group.Name]Config{},
		pid:        map[group.Name]int{},
		taskStatus: map[int]string{},
		nacks:      map[int]int{},
	}
	m.groups[group.Star] = &naiveGroup{pr: -1, builtin: true, policy: group.Policy{}, members: map[group.Name]struct{}{}}
	return m
}

func (m *naiveSim) validName(n group.Name) bool { return len(n) >= 1 && len(n) <= 64 }
func (m *naiveSim) validPR(pr int) bool         { return pr >= 0 && pr <= 1000 }

func (m *naiveSim) validateOp(op group.Op) error {
	switch op.Kind {
	case group.AddDevice, group.RemoveDevice:
		if !m.validName(op.Dev) {
			return group.ErrInvalid
		}
		_, ex := m.devices[op.Dev]
		if op.Kind == group.AddDevice && ex {
			return group.ErrExists
		}
		if op.Kind == group.RemoveDevice && !ex {
			return group.ErrNotFound
		}
	case group.AddGroup:
		if !m.validName(op.Group) || !m.validPR(op.PR) {
			return group.ErrInvalid
		}
		if _, ok := m.groups[op.Group]; ok {
			return group.ErrExists
		}
	case group.RemoveGroup:
		if !m.validName(op.Group) {
			return group.ErrInvalid
		}
		g, ok := m.groups[op.Group]
		if !ok {
			return group.ErrNotFound
		}
		if g.builtin {
			return group.ErrInvalid
		}
		if len(g.members) > 0 {
			return group.ErrNotEmpty
		}
	case group.AddMember, group.RemoveMember:
		if !m.validName(op.Group) || !m.validName(op.Dev) {
			return group.ErrInvalid
		}
		g, gok := m.groups[op.Group]
		if !gok {
			return group.ErrNotFound
		}
		if g.builtin {
			return group.ErrInvalid
		}
		d, dok := m.devices[op.Dev]
		if !dok {
			return group.ErrNotFound
		}
		_, member := d[op.Group]
		if op.Kind == group.AddMember {
			if member {
				return group.ErrExists
			}
			if len(d) >= m.gmax {
				return group.ErrTooManyGroups
			}
		} else if !member {
			return group.ErrNotFound
		}
	case group.SetPolicy:
		if !m.validName(op.Group) || len(op.Policy) > 32 {
			return group.ErrInvalid
		}
		for k := range op.Policy {
			if !m.validName(k) {
				return group.ErrInvalid
			}
		}
		if _, ok := m.groups[op.Group]; !ok {
			return group.ErrNotFound
		}
	case group.SetPriority:
		if !m.validName(op.Group) || !m.validPR(op.PR) {
			return group.ErrInvalid
		}
		g, ok := m.groups[op.Group]
		if !ok {
			return group.ErrNotFound
		}
		if g.builtin {
			return group.ErrInvalid
		}
	default:
		return group.ErrInvalid
	}
	return nil
}

// effective 朴素求值：遍历该设备所属分组（含 *），逐键选胜。
func (m *naiveSim) effective(dev group.Name) Config {
	type cand struct {
		pr  int
		gn  group.Name
		val group.Value
	}
	win := map[group.Name]cand{}
	consider := func(gn group.Name) {
		g := m.groups[gn]
		for k, v := range g.policy {
			c, ok := win[k]
			if !ok || g.pr > c.pr || (g.pr == c.pr && gn < c.gn) {
				win[k] = cand{pr: g.pr, gn: gn, val: v}
			}
		}
	}
	consider(group.Star)
	for gn := range m.devices[dev] {
		consider(gn)
	}
	out := Config{}
	for k, c := range win {
		if !c.val.Unset {
			out[k] = c.val.Val
		}
	}
	return out
}

// alignAll 朴素对齐：对全部现存设备重算。
func (m *naiveSim) alignAll() {
	names := make([]group.Name, 0, len(m.devices))
	for d := range m.devices {
		names = append(names, d)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	for _, d := range names {
		e := m.effective(d)
		if cfgEq(e, m.ack[d]) {
			if old := m.pid[d]; old != 0 {
				m.taskStatus[old] = "Cancelled"
				m.pid[d] = 0
				delete(m.pd, d)
			}
			continue
		}
		if cur, ok := m.pd[d]; ok && cfgEq(e, cur) {
			continue
		}
		if old := m.pid[d]; old != 0 {
			m.taskStatus[old] = "Superseded"
		}
		m.next++
		m.pid[d] = m.next
		m.pd[d] = cloneConfig(e)
		m.taskStatus[m.next] = "Pending"
	}
}

func cfgEq(a, b Config) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		bv, ok := b[k]
		if !ok || bv != v {
			return false
		}
	}
	return true
}

// Apply 返回 (失败下标, 原因)；成功下标为 -1。
func (m *naiveSim) Apply(ops []group.Op) (int, string) {
	if len(ops) < 1 || len(ops) > 256 {
		return -1, shortErr(group.ErrInvalid)
	}
	saved := m.clone()
	for i, op := range ops {
		if err := m.validateOp(op); err != nil {
			m.restore(saved)
			return i, shortErr(err)
		}
		m.exec(op)
	}
	m.alignAll()
	return -1, ""
}

func (m *naiveSim) exec(op group.Op) {
	switch op.Kind {
	case group.AddDevice:
		m.devices[op.Dev] = map[group.Name]struct{}{}
		m.ack[op.Dev] = Config{}
	case group.RemoveDevice:
		if old := m.pid[op.Dev]; old != 0 {
			m.taskStatus[old] = "Cancelled"
		}
		for g := range m.devices[op.Dev] {
			delete(m.groups[g].members, op.Dev)
		}
		delete(m.devices, op.Dev)
		delete(m.ack, op.Dev)
		delete(m.pd, op.Dev)
		delete(m.pid, op.Dev)
	case group.AddGroup:
		m.groups[op.Group] = &naiveGroup{pr: op.PR, policy: group.Policy{}, members: map[group.Name]struct{}{}}
	case group.RemoveGroup:
		delete(m.groups, op.Group)
	case group.AddMember:
		m.devices[op.Dev][op.Group] = struct{}{}
		m.groups[op.Group].members[op.Dev] = struct{}{}
	case group.RemoveMember:
		delete(m.devices[op.Dev], op.Group)
		delete(m.groups[op.Group].members, op.Dev)
	case group.SetPolicy:
		p := group.Policy{}
		for k, v := range op.Policy {
			p[k] = v
		}
		m.groups[op.Group].policy = p
	case group.SetPriority:
		m.groups[op.Group].pr = op.PR
	}
}

func (m *naiveSim) clone() *naiveSim {
	c := newNaive(m.gmax)
	for d, mem := range m.devices {
		nm := map[group.Name]struct{}{}
		for g := range mem {
			nm[g] = struct{}{}
		}
		c.devices[d] = nm
	}
	for n, g := range m.groups {
		ng := &naiveGroup{pr: g.pr, builtin: g.builtin, policy: clonePolicyN(g.policy), members: map[group.Name]struct{}{}}
		for mm := range g.members {
			ng.members[mm] = struct{}{}
		}
		c.groups[n] = ng
	}
	for d, v := range m.ack {
		c.ack[d] = cloneConfig(v)
	}
	for d, v := range m.pd {
		c.pd[d] = cloneConfig(v)
	}
	for d, v := range m.pid {
		c.pid[d] = v
	}
	for k, v := range m.taskStatus {
		c.taskStatus[k] = v
	}
	for k, v := range m.nacks {
		c.nacks[k] = v
	}
	c.next = m.next
	return c
}

func (m *naiveSim) restore(c *naiveSim) { *m = *c }

func clonePolicyN(p group.Policy) group.Policy {
	out := group.Policy{}
	for k, v := range p {
		out[k] = v
	}
	return out
}

func shortErr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (m *naiveSim) doAck(dev group.Name, id int) string {
	cur, ok := m.pid[dev]
	if !ok || cur == 0 || cur != id {
		return shortErr(ErrStale)
	}
	m.ack[dev] = cloneConfig(m.pd[dev])
	m.taskStatus[id] = "Acked"
	delete(m.pd, dev)
	delete(m.pid, dev)
	return ""
}

func (m *naiveSim) doNack(dev group.Name, id int) string {
	cur, ok := m.pid[dev]
	if !ok || cur == 0 || cur != id {
		return shortErr(ErrStale)
	}
	m.nacks[id]++
	return ""
}

func (m *naiveSim) nackCount(id int) int { return m.nacks[id] }

// ---- 随机操作生成 ----

type rngGen struct {
	rng    *rand.Rand
	gmax   int
	groups []group.Name // 已知非 * 分组名
	devs   []group.Name // 已知设备名
	keys   []group.Name
}

func newRngGen(seed int64, gmax int) *rngGen {
	return &rngGen{
		rng:  rand.New(rand.NewSource(seed)),
		gmax: gmax,
		keys: []group.Name{"interval", "log", "mode", "k1", "k2", "k3"},
	}
}

func (g *rngGen) name(prefix string, n int) group.Name {
	return group.Name(fmt.Sprintf("%s%d", prefix, g.rng.Intn(n)))
}

func (g *rngGen) pickOrGhost(pool []group.Name, prefix string, n int) group.Name {
	if len(pool) > 0 && g.rng.Intn(4) != 0 {
		return pool[g.rng.Intn(len(pool))]
	}
	return g.name(prefix, n)
}

func (g *rngGen) genPolicy() group.Policy {
	p := group.Policy{}
	n := g.rng.Intn(len(g.keys) + 1)
	used := map[int]bool{}
	for i := 0; i < n; i++ {
		ki := g.rng.Intn(len(g.keys))
		if used[ki] {
			continue
		}
		used[ki] = true
		switch g.rng.Intn(4) {
		case 0:
			p[g.keys[ki]] = group.Value{Unset: true}
		case 1:
			p[g.keys[ki]] = group.Value{Val: ""}
		default:
			p[g.keys[ki]] = group.Value{Val: fmt.Sprintf("v%d", g.rng.Intn(4))}
		}
	}
	return p
}

func (g *rngGen) genOne() group.Op {
	switch g.rng.Intn(10) {
	case 0:
		return group.Op{Kind: group.AddDevice, Dev: g.name("dev", 5)}
	case 1:
		return group.Op{Kind: group.RemoveDevice, Dev: g.pickOrGhost(g.devs, "dev", 5)}
	case 2:
		return group.Op{Kind: group.AddGroup, Group: g.name("grp", 5), PR: g.rng.Intn(13) - 1}
	case 3:
		return group.Op{Kind: group.RemoveGroup, Group: g.pickOrGhost(g.groups, "grp", 5)}
	case 4:
		return group.Op{Kind: group.AddMember, Group: g.pickOrGhost(g.groups, "grp", 5), Dev: g.pickOrGhost(g.devs, "dev", 5)}
	case 5:
		return group.Op{Kind: group.RemoveMember, Group: g.pickOrGhost(g.groups, "grp", 5), Dev: g.pickOrGhost(g.devs, "dev", 5)}
	case 6, 7:
		gn := group.Star
		if g.rng.Intn(2) == 0 {
			gn = g.pickOrGhost(g.groups, "grp", 5)
		}
		return group.Op{Kind: group.SetPolicy, Group: gn, Policy: g.genPolicy()}
	default:
		return group.Op{Kind: group.SetPriority, Group: g.pickOrGhost(g.groups, "grp", 5), PR: g.rng.Intn(13) - 1}
	}
}

func (g *rngGen) genBatch() []group.Op {
	ops := make([]group.Op, 1+g.rng.Intn(4))
	for i := range ops {
		ops[i] = g.genOne()
	}
	return ops
}

// refresh 从真实快照同步名字池（供随机 Ack 选择）。
func (g *rngGen) refresh(snap group.Snapshot) {
	g.devs = g.devs[:0]
	for d := range snap.Devices {
		g.devs = append(g.devs, d)
	}
	sort.Slice(g.devs, func(i, j int) bool { return g.devs[i] < g.devs[j] })
	g.groups = g.groups[:0]
	for n, gs := range snap.Groups {
		if !gs.Builtin {
			g.groups = append(g.groups, n)
		}
	}
	sort.Slice(g.groups, func(i, j int) bool { return g.groups[i] < g.groups[j] })
}

func opString(op group.Op) string {
	switch op.Kind {
	case group.AddDevice:
		return fmt.Sprintf("AddDevice(%q)", op.Dev)
	case group.RemoveDevice:
		return fmt.Sprintf("RemoveDevice(%q)", op.Dev)
	case group.AddGroup:
		return fmt.Sprintf("AddGroup(%q,%d)", op.Group, op.PR)
	case group.RemoveGroup:
		return fmt.Sprintf("RemoveGroup(%q)", op.Group)
	case group.AddMember:
		return fmt.Sprintf("AddMember(%q,%q)", op.Group, op.Dev)
	case group.RemoveMember:
		return fmt.Sprintf("RemoveMember(%q,%q)", op.Group, op.Dev)
	case group.SetPolicy:
		parts := make([]string, 0, len(op.Policy))
		keys := make([]group.Name, 0, len(op.Policy))
		for k := range op.Policy {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		for _, k := range keys {
			v := op.Policy[k]
			if v.Unset {
				parts = append(parts, string(k)+":Unset")
			} else {
				parts = append(parts, string(k)+":"+v.Val)
			}
		}
		return fmt.Sprintf("SetPolicy(%q,{%s})", op.Group, strings.Join(parts, ","))
	case group.SetPriority:
		return fmt.Sprintf("SetPriority(%q,%d)", op.Group, op.PR)
	}
	return "?"
}

func cfgString(c Config) string {
	keys := make([]group.Name, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, string(k)+"="+c[k])
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// compareStates 比对真实服务与朴素模拟的全部可观测状态，返回差异描述。
func compareStates(t *testing.T, s *Service, m *naiveSim) string {
	t.Helper()
	snap := s.Store().Snapshot()

	if len(snap.Devices) != len(m.devices) {
		return fmt.Sprintf("device set: real=%d naive=%d", len(snap.Devices), len(m.devices))
	}
	for d, ds := range snap.Devices {
		mm, ok := m.devices[d]
		if !ok {
			return "device " + string(d) + " missing in naive"
		}
		if len(ds.Groups) != len(mm) {
			return fmt.Sprintf("device %s membership size real=%d naive=%d", d, len(ds.Groups), len(mm))
		}
		for _, g := range ds.Groups {
			if _, ok := mm[g]; !ok {
				return fmt.Sprintf("device %s missing membership %s in naive", d, g)
			}
		}
		// E
		e := s.EffectiveConfig(d)
		ne := m.effective(d)
		if !cfgEq(e, ne) {
			return fmt.Sprintf("E(%s): real=%s naive=%s", d, cfgString(e), cfgString(ne))
		}
		// A
		if !cfgEq(s.AckedConfig(d), m.ack[d]) {
			return fmt.Sprintf("A(%s): real=%s naive=%s", d, cfgString(s.AckedConfig(d)), cfgString(m.ack[d]))
		}
		// Pd
		realPid := s.PendingID(d)
		if realPid != m.pid[d] {
			return fmt.Sprintf("pid(%s): real=%d naive=%d", d, realPid, m.pid[d])
		}
		if realPid != 0 {
			pc := s.PendingConfig(d)
			if !cfgEq(pc, m.pd[d]) {
				return fmt.Sprintf("pd(%s): real=%s naive=%s", d, cfgString(pc), cfgString(m.pd[d]))
			}
			if st := s.TaskStatus(realPid); st != Pending {
				return fmt.Sprintf("pid %d not Pending in real: %s", realPid, st)
			}
		}
	}
	if s.NextPushID() != m.next {
		return fmt.Sprintf("next pushId: real=%d naive=%d", s.NextPushID(), m.next)
	}
	for pid := 1; pid <= m.next; pid++ {
		want := m.taskStatus[pid]
		got := s.TaskStatus(pid).String()
		if got != want {
			return fmt.Sprintf("task %d: real=%s naive=%s", pid, got, want)
		}
	}
	// 分组结构
	if len(snap.Groups) != len(m.groups) {
		return fmt.Sprintf("group count: real=%d naive=%d", len(snap.Groups), len(m.groups))
	}
	for n, gs := range snap.Groups {
		mg, ok := m.groups[n]
		if !ok {
			return "group " + string(n) + " missing in naive"
		}
		if gs.PR != mg.pr || len(gs.Members) != len(mg.members) || !policyEqual(gs.Policy, mg.policy) {
			return fmt.Sprintf("group %s differs", n)
		}
	}
	return ""
}

// TestRandomDifferential 1500 组随机操作序列，与全量重算的朴素模拟逐状态对照。
// 每个序列固定种子可重放；日志记录输入、输出与判定依据，失败时完整转储。
func TestRandomDifferential(t *testing.T) {
	const scenarios = 1500
	for seed := int64(0); seed < scenarios; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			gmax := 1 + int(seed%16)
			gen := newRngGen(seed, gmax)
			real := New(gmax)
			naive := newNaive(gmax)

			var log strings.Builder
			fmt.Fprintf(&log, "seed=%d gmax=%d\n", seed, gmax)

			const steps = 24
			for step := 0; step < steps; step++ {
				// 30% 概率插入 Ack/Nack（含错误编号），70% 变更批次。
				gen.refresh(real.Store().Snapshot())
				if len(gen.devs) > 0 && gen.rng.Intn(10) < 3 {
					d := gen.devs[gen.rng.Intn(len(gen.devs))]
					realCur := real.PendingID(d)
					id := realCur
					if gen.rng.Intn(3) == 0 {
						if id == 0 {
							id = 1 + gen.rng.Intn(real.NextPushID()+2)
						} else if gen.rng.Intn(2) == 0 {
							id++
						}
					}
					var realErr, naiveErr string
					if gen.rng.Intn(2) == 0 {
						fmt.Fprintf(&log, "step%d Ack(%q,%d) cur=%d\n", step, d, id, realCur)
						if err := real.Ack(d, id); err != nil {
							realErr = err.Error()
						}
						naiveErr = naive.doAck(d, id)
					} else {
						fmt.Fprintf(&log, "step%d Nack(%q,%d) cur=%d\n", step, d, id, realCur)
						if err := real.Nack(d, id); err != nil {
							realErr = err.Error()
						}
						naiveErr = naive.doNack(d, id)
					}
					if realErr != naiveErr {
						t.Fatalf("seed %d step %d ack/nack error: real=%q naive=%q\n%s", seed, step, realErr, naiveErr, log.String())
					}
					if realErr == "" && real.NackCount(id) != naive.nackCount(id) {
						t.Fatalf("seed %d step %d nack count: real=%d naive=%d\n%s",
							seed, step, real.NackCount(id), naive.nackCount(id), log.String())
					}
				} else {
					ops := gen.genBatch()
					parts := make([]string, len(ops))
					for i, op := range ops {
						parts[i] = opString(op)
					}
					err := real.Apply(ops)
					nIdx, nErr := naive.Apply(ops)
					if err != nil {
						var oe *group.OpError
						if !errors.As(err, &oe) {
							t.Fatalf("seed %d step %d: non-OpError %v\n%s", seed, step, err, log.String())
						}
						fmt.Fprintf(&log, "step%d Apply[%s] -> REJECT op[%d] %s (naive op[%d] %s)\n",
							step, strings.Join(parts, ";"), oe.Index, oe.Err, nIdx, nErr)
						if oe.Index != nIdx || oe.Err.Error() != nErr {
							t.Fatalf("seed %d step %d reject mismatch: real=op[%d] %s naive=op[%d] %s\n%s",
								seed, step, oe.Index, oe.Err, nIdx, nErr, log.String())
						}
					} else {
						fmt.Fprintf(&log, "step%d Apply[%s] -> ACCEPT next=%d (naive next=%d)\n",
							step, strings.Join(parts, ";"), real.NextPushID(), nIdxCheck(naive))
						if nIdx != -1 {
							t.Fatalf("seed %d step %d: real accepted but naive rejected op[%d] %s\n%s",
								seed, step, nIdx, nErr, log.String())
						}
					}
				}

				if msg := compareStates(t, real, naive); msg != "" {
					gen.refresh(real.Store().Snapshot())
					for _, d := range gen.devs {
						fmt.Fprintf(&log, "  state %s: E=%s A=%s pid=%d\n",
							d, cfgString(real.EffectiveConfig(d)), cfgString(real.AckedConfig(d)), real.PendingID(d))
					}
					t.Fatalf("seed %d step %d divergence: %s\n%s", seed, step, msg, log.String())
				}
				if msg := real.CheckInvariant(); msg != "" {
					t.Fatalf("seed %d step %d invariant broken: %s\n%s", seed, step, msg, log.String())
				}
			}

			// 判定依据：用同种子重新生成并执行同一序列，结果必须与首次完全一致。
			gen2 := newRngGen(seed, gmax)
			real2 := New(gmax)
			for step := 0; step < steps; step++ {
				gen2.refresh(real2.Store().Snapshot())
				if len(gen2.devs) > 0 && gen2.rng.Intn(10) < 3 {
					d := gen2.devs[gen2.rng.Intn(len(gen2.devs))]
					id := real2.PendingID(d)
					if gen2.rng.Intn(3) == 0 {
						if id == 0 {
							id = 1 + gen2.rng.Intn(real2.NextPushID()+2)
						} else if gen2.rng.Intn(2) == 0 {
							id++
						}
					}
					if gen2.rng.Intn(2) == 0 {
						_ = real2.Ack(d, id)
					} else {
						_ = real2.Nack(d, id)
					}
				} else {
					_ = real2.Apply(gen2.genBatch())
				}
			}
			if msg := compareReal(t, real, real2); msg != "" {
				t.Fatalf("seed %d replay diverges: %s\n%s", seed, msg, log.String())
			}
			t.Logf("seed %d OK: gmax=%d final devices=%d groups=%d nextPushId=%d — 输入/输出/判定: 全量逐设备状态与朴素模拟一致，且同序列重放一致",
				seed, gmax, len(real.Store().Snapshot().Devices), len(real.Store().Snapshot().Groups), real.NextPushID())
		})
	}
}

func nIdxCheck(m *naiveSim) int { return m.next }

// compareReal 比较两次同序列重放的真实服务结果。
func compareReal(t *testing.T, a, b *Service) string {
	t.Helper()
	sa := a.Store().Snapshot()
	sb := b.Store().Snapshot()
	if len(sa.Devices) != len(sb.Devices) {
		return fmt.Sprintf("device count %d vs %d", len(sa.Devices), len(sb.Devices))
	}
	for d := range sa.Devices {
		if _, ok := sb.Devices[d]; !ok {
			return "device " + string(d) + " missing on replay"
		}
		if !cfgEq(a.EffectiveConfig(d), b.EffectiveConfig(d)) {
			return fmt.Sprintf("E(%s) %s vs %s", d, cfgString(a.EffectiveConfig(d)), cfgString(b.EffectiveConfig(d)))
		}
		if !cfgEq(a.AckedConfig(d), b.AckedConfig(d)) {
			return "A(" + string(d) + ") differs"
		}
		if a.PendingID(d) != b.PendingID(d) {
			return fmt.Sprintf("pid(%s) %d vs %d", d, a.PendingID(d), b.PendingID(d))
		}
		if a.PendingID(d) != 0 && !cfgEq(a.PendingConfig(d), b.PendingConfig(d)) {
			return "Pd(" + string(d) + ") differs"
		}
	}
	if a.NextPushID() != b.NextPushID() {
		return fmt.Sprintf("next %d vs %d", a.NextPushID(), b.NextPushID())
	}
	for pid := 1; pid <= a.NextPushID(); pid++ {
		if a.TaskStatus(pid) != b.TaskStatus(pid) || a.NackCount(pid) != b.NackCount(pid) {
			return fmt.Sprintf("task %d %s(%d) vs %s(%d)",
				pid, a.TaskStatus(pid), a.NackCount(pid), b.TaskStatus(pid), b.NackCount(pid))
		}
	}
	return ""
}
