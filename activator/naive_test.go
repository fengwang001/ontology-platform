package activator

import (
	"math/rand"
	"sort"
	"testing"

	"ontology/config"
)

// naiveModel 是严格按题面规则逐步书写的朴素参考实现：
// 每步先在可变状态上“试跑”，校验失败即丢弃整个试跑状态（NACK 不落盘）。
type naiveModel struct {
	W            int64
	maxNow       int64
	lastAccepted int64

	clusters map[string]*naiveCluster
	routes   map[string]*naiveRoute
}

type naiveCluster struct {
	serving    int64
	hasServing bool
	warming    int64
	hasWarming bool
	since      int64
}

type naiveRoute struct {
	servingVer  int64
	hasServing  bool
	servingRefs []string
	pendingVer  int64
	hasPending  bool
	pendingRefs []string
}

func newNaive(W int64) *naiveModel {
	return &naiveModel{
		W:        W,
		clusters: map[string]*naiveCluster{},
		routes:   map[string]*naiveRoute{},
	}
}

func (m *naiveModel) clone() *naiveModel {
	n := &naiveModel{
		W:            m.W,
		maxNow:       m.maxNow,
		lastAccepted: m.lastAccepted,
		clusters:     map[string]*naiveCluster{},
		routes:       map[string]*naiveRoute{},
	}
	for k, c := range m.clusters {
		cp := *c
		n.clusters[k] = &cp
	}
	for k, r := range m.routes {
		cp := *r
		cp.servingRefs = append([]string(nil), r.servingRefs...)
		cp.pendingRefs = append([]string(nil), r.pendingRefs...)
		n.routes[k] = &cp
	}
	return n
}

// op 是可重放的统一操作记录。
type op struct {
	kind string // push | ready | serving | state
	ver  int64
	push config.Push
	name string
	now  int64
}

type opResult struct {
	errKind string // "" | invalid | time | rewind | version | dangling | inuse | notwarming
	serving int64
	hasS    bool
	present bool
	sVer    int64
	wVer    int64
	since   int64
	hasW    bool
}

func errKind(err error) string {
	switch err {
	case nil:
		return ""
	case config.ErrInvalidParam:
		return "invalid"
	case config.ErrInvalidTime:
		return "time"
	case config.ErrClockRewind:
		return "rewind"
	case config.ErrBadVersion:
		return "version"
	case config.ErrDanglingRef:
		return "dangling"
	case config.ErrInUse:
		return "inuse"
	case config.ErrNotWarming:
		return "notwarming"
	default:
		return "other:" + err.Error()
	}
}

func nHasDup(xs []string) bool {
	seen := map[string]bool{}
	for _, x := range xs {
		if seen[x] {
			return true
		}
		seen[x] = true
	}
	return false
}

func nContains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// apply 在克隆上执行；失败返回错误且 m 不变。
func (m *naiveModel) apply(o op) opResult {
	switch o.kind {
	case "push":
		return m.applyPush(o)
	case "ready":
		return m.applyReady(o)
	case "serving", "state":
		return m.applyQuery(o)
	}
	panic("bad op kind")
}

func (m *naiveModel) settle(t *naiveModel, now int64) {
	type tm struct {
		name  string
		since int64
	}
	var tms []tm
	for n, c := range t.clusters {
		if c.hasWarming && c.since+t.W <= now {
			tms = append(tms, tm{n, c.since})
		}
	}
	sort.Slice(tms, func(i, j int) bool {
		if tms[i].since != tms[j].since {
			return tms[i].since < tms[j].since
		}
		return tms[i].name < tms[j].name
	})
	for _, e := range tms {
		c := t.clusters[e.name]
		if c == nil || !c.hasWarming || c.since+t.W > now {
			continue
		}
		c.hasWarming = false
		c.warming = 0
		c.since = 0
		if !c.hasServing {
			delete(t.clusters, e.name)
			for rn, rt := range t.routes {
				if rt.hasPending && nContains(rt.pendingRefs, e.name) {
					rt.hasPending = false
					rt.pendingVer = 0
					rt.pendingRefs = nil
				}
				_ = rn
			}
		}
	}
	// 固定点级联：所有引用集群均就绪的待激活路由转在役。
	t.cascadeAll()
}

func (t *naiveModel) clusterReady(name string) bool {
	c := t.clusters[name]
	return c != nil && c.hasServing && !c.hasWarming
}

func (t *naiveModel) cascadeAll() {
	for {
		changed := false
		names := make([]string, 0, len(t.routes))
		for n, rt := range t.routes {
			if rt.hasPending {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		for _, n := range names {
			rt := t.routes[n]
			if !rt.hasPending {
				continue
			}
			ok := true
			for _, c := range rt.pendingRefs {
				if !t.clusterReady(c) {
					ok = false
					break
				}
			}
			if ok {
				rt.servingVer = rt.pendingVer
				rt.hasServing = true
				rt.servingRefs = append([]string(nil), rt.pendingRefs...)
				rt.hasPending = false
				rt.pendingVer = 0
				rt.pendingRefs = nil
				changed = true
			}
		}
		if !changed {
			return
		}
	}
}

func (m *naiveModel) applyPush(o op) opResult {
	p := o.push
	// 参数校验
	for _, n := range p.ClusterUpserts {
		if n == "" {
			return opResult{errKind: "invalid"}
		}
	}
	for _, r := range p.RouteUpserts {
		if r.Name == "" || len(r.Refs) < 1 || len(r.Refs) > 8 || nHasDup(r.Refs) {
			return opResult{errKind: "invalid"}
		}
		for _, c := range r.Refs {
			if c == "" {
				return opResult{errKind: "invalid"}
			}
		}
	}
	for _, n := range append(append([]string(nil), p.ClusterDeletes...), p.RouteDeletes...) {
		if n == "" {
			return opResult{errKind: "invalid"}
		}
	}
	if nHasDup(p.ClusterUpserts) || nHasDup(p.ClusterDeletes) || nHasDup(p.RouteDeletes) {
		return opResult{errKind: "invalid"}
	}
	rseen := map[string]bool{}
	for _, r := range p.RouteUpserts {
		if rseen[r.Name] {
			return opResult{errKind: "invalid"}
		}
		rseen[r.Name] = true
	}
	cdel := map[string]bool{}
	for _, n := range p.ClusterDeletes {
		cdel[n] = true
	}
	for _, n := range p.ClusterUpserts {
		if cdel[n] {
			return opResult{errKind: "invalid"}
		}
	}
	rdel := map[string]bool{}
	for _, n := range p.RouteDeletes {
		rdel[n] = true
	}
	for _, r := range p.RouteUpserts {
		if rdel[r.Name] {
			return opResult{errKind: "invalid"}
		}
	}
	// 时间
	if o.now < 0 || o.now > 1_000_000_000_000_000 {
		return opResult{errKind: "time"}
	}
	if o.now < m.maxNow {
		return opResult{errKind: "rewind"}
	}
	// 序号
	if o.ver != m.lastAccepted+1 {
		return opResult{errKind: "version"}
	}

	t := m.clone()
	m.settle(t, o.now)

	// 推送后集群集合
	post := map[string]bool{}
	for n := range t.clusters {
		if !cdel[n] {
			post[n] = true
		}
	}
	for _, n := range p.ClusterUpserts {
		post[n] = true
	}
	// 悬空
	for _, r := range p.RouteUpserts {
		for _, c := range r.Refs {
			if !post[c] {
				return opResult{errKind: "dangling"}
			}
		}
	}
	// 在用
	for _, dc := range p.ClusterDeletes {
		for rn, rt := range t.routes {
			if rdel[rn] {
				continue
			}
			if up, ok := findRouteUp(p.RouteUpserts, rn); ok {
				if nContains(up.Refs, dc) {
					return opResult{errKind: "inuse"}
				}
				if rt.hasServing && nContains(rt.servingRefs, dc) {
					return opResult{errKind: "inuse"}
				}
				continue
			}
			if rt.hasServing && nContains(rt.servingRefs, dc) {
				return opResult{errKind: "inuse"}
			}
			if rt.hasPending && nContains(rt.pendingRefs, dc) {
				return opResult{errKind: "inuse"}
			}
		}
	}

	// 应用：先删路由
	for _, n := range p.RouteDeletes {
		delete(t.routes, n)
	}
	// 集群更新进预热
	for _, n := range p.ClusterUpserts {
		c := t.clusters[n]
		if c == nil {
			c = &naiveCluster{}
			t.clusters[n] = c
		}
		c.warming = o.ver
		c.hasWarming = true
		c.since = o.now
	}
	// 删集群
	for _, n := range p.ClusterDeletes {
		delete(t.clusters, n)
	}
	// 路由待激活
	for _, ru := range p.RouteUpserts {
		rt := t.routes[ru.Name]
		if rt == nil {
			rt = &naiveRoute{}
			t.routes[ru.Name] = rt
		}
		rt.pendingVer = o.ver
		rt.hasPending = true
		rt.pendingRefs = append([]string(nil), ru.Refs...)
	}
	t.lastAccepted = o.ver
	t.cascadeAll()
	t.maxNow = o.now
	*m = *t
	return opResult{}
}

func findRouteUp(ups []config.Route, name string) (config.Route, bool) {
	for _, u := range ups {
		if u.Name == name {
			return u, true
		}
	}
	return config.Route{}, false
}

func (m *naiveModel) applyReady(o op) opResult {
	if o.name == "" {
		return opResult{errKind: "invalid"}
	}
	if o.now < 0 || o.now > 1_000_000_000_000_000 {
		return opResult{errKind: "time"}
	}
	if o.now < m.maxNow {
		return opResult{errKind: "rewind"}
	}
	t := m.clone()
	m.settle(t, o.now)
	c := t.clusters[o.name]
	if c == nil || !c.hasWarming {
		return opResult{errKind: "notwarming"}
	}
	c.serving = c.warming
	c.hasServing = true
	c.hasWarming = false
	c.warming = 0
	c.since = 0
	t.cascadeAll()
	t.maxNow = o.now
	*m = *t
	return opResult{}
}

func (m *naiveModel) applyQuery(o op) opResult {
	if o.now < 0 || o.now > 1_000_000_000_000_000 {
		return opResult{errKind: "time"}
	}
	if o.now < m.maxNow {
		return opResult{errKind: "rewind"}
	}
	t := m.clone()
	m.settle(t, o.now)
	res := opResult{}
	if o.kind == "serving" {
		rt := t.routes[o.name]
		if rt != nil && rt.hasServing {
			res.serving, res.hasS = rt.servingVer, true
		}
	} else {
		if c := t.clusters[o.name]; c != nil {
			res.present = true
			res.sVer = c.serving
			res.wVer = c.warming
			res.since = c.since
			res.hasW = c.hasWarming
		}
	}
	t.maxNow = o.now
	*m = *t
	return res
}

// ---- 随机序列生成与对照 ----

func genOps(rng *rand.Rand, n int, m *naiveModel) []op {
	const nC, nR = 4, 5
	clusterNames := []string{"c1", "c2", "c3", "c4"}
	routeNames := []string{"r1", "r2", "r3", "r4", "r5"}
	var ops []op
	var nextVer int64 = 1
	var now int64
	for i := 0; i < n; i++ {
		// 时间：通常前进，偶发回退/非法。
		switch rng.Intn(10) {
		case 0:
			now += int64(rng.Intn(20))
		case 9:
			now -= int64(rng.Intn(3))
		default:
			now += int64(rng.Intn(12))
		}
		if now < 0 {
			now = 0
		}
		kind := []string{"push", "push", "push", "ready", "serving", "state"}[rng.Intn(6)]
		switch kind {
		case "push":
			ver := nextVer
			if rng.Intn(8) == 0 {
				ver += int64(1 + rng.Intn(3)) // 故意跳号
			}
			p := config.Push{}
			upsertSet := map[string]bool{}
			if rng.Intn(2) == 0 {
				k := 1 + rng.Intn(nC)
				for j := 0; j < k; j++ {
					c := clusterNames[rng.Intn(nC)]
					if !upsertSet[c] {
						upsertSet[c] = true
						p.ClusterUpserts = append(p.ClusterUpserts, c)
					}
				}
			}
			if rng.Intn(2) == 0 {
				rs := map[string]bool{}
				k := 1 + rng.Intn(3)
				for j := 0; j < k; j++ {
					rn := routeNames[rng.Intn(nR)]
					if rs[rn] {
						continue
					}
					rs[rn] = true
					kc := 1 + rng.Intn(3)
					refs := []string{}
					seen := map[string]bool{}
					for len(refs) < kc {
						c := clusterNames[rng.Intn(nC)]
						if !seen[c] {
							seen[c] = true
							refs = append(refs, c)
						}
					}
					p.RouteUpserts = append(p.RouteUpserts, config.Route{Name: rn, Refs: refs})
				}
			}
			// 删除：从已知存在或任意名字中选。
			if rng.Intn(3) == 0 {
				p.ClusterDeletes = append(p.ClusterDeletes, clusterNames[rng.Intn(nC)])
			}
			if rng.Intn(3) == 0 {
				p.RouteDeletes = append(p.RouteDeletes, routeNames[rng.Intn(nR)])
			}
			o := op{kind: "push", ver: ver, push: p, now: now}
			ops = append(ops, o)
			// 用朴素模型预演以跟踪真实序号（仅用于生成后续合法序号，不影响判定）。
			before := m.lastAccepted
			m.apply(o)
			if m.lastAccepted != before {
				nextVer = m.lastAccepted + 1
			}
		case "ready":
			name := clusterNames[rng.Intn(nC)]
			if rng.Intn(15) == 0 {
				name = "ghost"
			}
			ops = append(ops, op{kind: "ready", name: name, now: now})
		case "serving":
			name := routeNames[rng.Intn(nR)]
			ops = append(ops, op{kind: "serving", name: name, now: now})
		case "state":
			name := clusterNames[rng.Intn(nC)]
			ops = append(ops, op{kind: "state", name: name, now: now})
		}
	}
	return ops
}

func runReal(a *Activator, o op) opResult {
	switch o.kind {
	case "push":
		return opResult{errKind: errKind(a.Push(o.ver, o.push, o.now))}
	case "ready":
		return opResult{errKind: errKind(a.Ready(o.name, o.now))}
	case "serving":
		v, ok, err := a.Serving(o.name, o.now)
		return opResult{errKind: errKind(err), serving: v, hasS: ok}
	case "state":
		st, err := a.State(o.name, o.now)
		return opResult{
			errKind: errKind(err),
			present: st.Present,
			sVer:    st.Version,
			wVer:    st.WarmingVersion,
			since:   st.Since,
			hasW:    st.HasWarming,
		}
	}
	panic("bad kind")
}

func sameResult(x, y opResult) bool {
	return x == y
}

func TestRandomVsNaive(t *testing.T) {
	const groups = 2000
	const opsPerGroup = 60
	rng := rand.New(rand.NewSource(20261003))
	for g := 0; g < groups; g++ {
		gen := newNaive(10)
		ops := genOps(rng, opsPerGroup, gen)
		real := New(10)
		naive := newNaive(10)
		var log []string
		var firstResults []opResult
		fail := false
		for i, o := range ops {
			rv := runReal(real, o)
			nv := naive.apply(o)
			if !sameResult(rv, nv) {
				t.Errorf("group %d op %d mismatch:\n op=%+v\n real=%+v\nnaive=%+v", g, i, o, rv, nv)
				fail = true
				break
			}
			firstResults = append(firstResults, rv)
			log = append(log, formatOp(o, rv))
		}
		// 终态一致。
		rd, nd := real.dump(), naiveDump(naive)
		if rd != nd {
			t.Errorf("group %d final state mismatch:\nreal:\n%s\nnaive:\n%s", g, rd, nd)
			fail = true
		}
		if fail {
			for _, l := range log {
				t.Log(l)
			}
			return
		}
		// 重放确定性：干净实例重放同一序列，逐结果与终态一致。
		replay := New(10)
		for i, o := range ops {
			rv := runReal(replay, o)
			if rv != firstResults[i] {
				t.Fatalf("group %d op %d replay differs: %+v first=%+v replay=%+v", g, i, o, firstResults[i], rv)
			}
		}
		if replay.dump() != rd {
			t.Fatalf("group %d replay final state differs", g)
		}
	}
}

func formatOp(o op, r opResult) string {
	switch o.kind {
	case "push":
		return "PUSH ver=" + itoa(o.ver) + " now=" + itoa(o.now) +
			" upC=" + fmtSlice(o.push.ClusterUpserts) +
			" upR=" + fmtRoutes(o.push.RouteUpserts) +
			" delC=" + fmtSlice(o.push.ClusterDeletes) +
			" delR=" + fmtSlice(o.push.RouteDeletes) +
			" => " + r.errKind
	case "ready":
		return "READY " + o.name + " now=" + itoa(o.now) + " => " + r.errKind
	case "serving":
		return "SERVING " + o.name + " now=" + itoa(o.now) + " => " + r.errKind +
			" v=" + itoa(r.serving) + " ok=" + boolStr(r.hasS)
	default:
		return "STATE " + o.name + " now=" + itoa(o.now) + " => " + r.errKind +
			" present=" + boolStr(r.present)
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func fmtSlice(xs []string) string {
	out := "["
	for i, x := range xs {
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out + "]"
}

func fmtRoutes(rs []config.Route) string {
	out := "{"
	for i, r := range rs {
		if i > 0 {
			out += ","
		}
		out += r.Name + ":" + fmtSlice(r.Refs)
	}
	return out + "}"
}

func naiveDump(m *naiveModel) string {
	var b []byte
	b = append(b, "now="+itoa(m.maxNow)+" ver="+itoa(m.lastAccepted)+"\n"...)
	names := make([]string, 0, len(m.clusters))
	for n := range m.clusters {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c := m.clusters[n]
		s := "false"
		if c.hasServing {
			s = "true"
		}
		w := "false"
		if c.hasWarming {
			w = "true"
		}
		b = append(b, ("C " + n + " s=" + s + "(" + itoa(c.serving) + ") w=" + w + "(" +
			itoa(c.warming) + "@" + itoa(c.since) + ")\n")...)
	}
	rns := make([]string, 0, len(m.routes))
	for n := range m.routes {
		rns = append(rns, n)
	}
	sort.Strings(rns)
	for _, n := range rns {
		rt := m.routes[n]
		s := "false"
		if rt.hasServing {
			s = "true"
		}
		p := "false"
		if rt.hasPending {
			p = "true"
		}
		b = append(b, ("R " + n + " s=" + s + "(" + itoa(rt.servingVer) + " refs=" + fmtRefs(rt.servingRefs) +
			") p=" + p + "(" + itoa(rt.pendingVer) + " refs=" + fmtRefs(rt.pendingRefs) + ")\n")...)
	}
	return string(b)
}

func fmtRefs(xs []string) string {
	out := "["
	for i, x := range xs {
		if i > 0 {
			out += " "
		}
		out += x
	}
	return out + "]"
}
