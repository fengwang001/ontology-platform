package promote_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"ontology/promote"
	"ontology/tracker"
)

// ---------- 逐步朴素模拟器（独立于 tracker 内部实现，按题目规则直写） ----------

type nOp struct {
	term int
	body string
	noop bool
}

type nMember struct {
	role tracker.Role
	proc map[int]bool
}

type naive struct {
	order        []string
	members      map[string]*nMember
	primary      string
	term         int
	maxSeq       int
	gcp          int
	ops          map[int]nOp
	confirmed    []int
	confirmedSet map[int]bool
	lost         []int
	lostVersions map[[2]int]bool
}

func newNaive(primary string, replicas []string) *naive {
	n := &naive{
		members:      map[string]*nMember{},
		primary:      primary,
		term:         1,
		ops:          map[int]nOp{},
		confirmedSet: map[int]bool{},
		lostVersions: map[[2]int]bool{},
	}
	n.put(primary, tracker.RolePrimary)
	for _, r := range replicas {
		n.put(r, tracker.RoleSync)
	}
	return n
}

func (n *naive) put(name string, role tracker.Role) {
	n.members[name] = &nMember{role: role, proc: map[int]bool{}}
	n.order = append(n.order, name)
}

func validName(name string) bool { return len(name) >= 1 && len(name) <= 64 }

func (n *naive) lcp(m *nMember) int {
	k := 0
	for m.proc[k+1] {
		k++
	}
	return k
}

func (n *naive) maxProc(m *nMember) int {
	mx := 0
	for seq := range m.proc {
		if seq > mx {
			mx = seq
		}
	}
	return mx
}

func (n *naive) synced() []*nMember {
	out := make([]*nMember, 0)
	for _, name := range n.order {
		m := n.members[name]
		if m.role == tracker.RolePrimary || m.role == tracker.RoleSync {
			out = append(out, m)
		}
	}
	return out
}

// recompute 朴素每步重算：gcp 取同步集合 lcp 最小值（只增不减）；
// 确认收集本步新达成全员处理的真实操作，按 seq 升序追加。
func (n *naive) recompute() {
	synced := n.synced()
	if len(synced) > 0 {
		minLCP := n.lcp(synced[0])
		for _, m := range synced[1:] {
			if k := n.lcp(m); k < minLCP {
				minLCP = k
			}
		}
		if minLCP > n.gcp {
			n.gcp = minLCP
		}
	}
	need := len(synced)
	ready := make([]int, 0)
	for seq := 1; seq <= n.maxSeq; seq++ {
		op := n.ops[seq]
		if op.noop || n.confirmedSet[seq] {
			continue
		}
		cnt := 0
		for _, m := range synced {
			if m.proc[seq] {
				cnt++
			}
		}
		if cnt == need {
			ready = append(ready, seq)
		}
	}
	sort.Ints(ready)
	for _, seq := range ready {
		n.confirmedSet[seq] = true
		n.confirmed = append(n.confirmed, seq)
	}
}

type action struct {
	kind      string
	name      string
	seq, term int
	body      string
}

func (n *naive) exec(a action) error {
	switch a.kind {
	case "write":
		seq := n.maxSeq + 1
		n.maxSeq = seq
		n.ops[seq] = nOp{term: n.term, body: a.body}
		n.members[n.primary].proc[seq] = true
		n.recompute()
		return nil
	case "ack":
		// 参数非法 > 成员不存在 > 任期过期 > 状态不符。
		if !validName(a.name) || a.seq < 1 || a.term < 1 ||
			a.seq > n.maxSeq || a.term > n.term {
			return tracker.ErrInvalidArg
		}
		m, ok := n.members[a.name]
		if !ok {
			return tracker.ErrMemberNotFound
		}
		if a.term < n.term {
			return tracker.ErrStaleTerm
		}
		if m.role == tracker.RolePrimary {
			return tracker.ErrWrongState
		}
		m.proc[a.seq] = true
		n.recompute()
		return nil
	case "add":
		if !validName(a.name) {
			return tracker.ErrInvalidArg
		}
		if _, ok := n.members[a.name]; ok {
			return tracker.ErrMemberExists
		}
		n.put(a.name, tracker.RoleCatchup)
		return nil
	case "sync":
		if !validName(a.name) {
			return tracker.ErrInvalidArg
		}
		m, ok := n.members[a.name]
		if !ok {
			return tracker.ErrMemberNotFound
		}
		if m.role != tracker.RoleCatchup {
			return tracker.ErrWrongState
		}
		if n.lcp(m) < n.gcp {
			return tracker.ErrNotCaughtUp
		}
		for seq := range n.confirmedSet {
			if !m.proc[seq] {
				return tracker.ErrNotCaughtUp
			}
		}
		m.role = tracker.RoleSync
		n.recompute()
		return nil
	case "fail":
		if !validName(a.name) {
			return tracker.ErrInvalidArg
		}
		m, ok := n.members[a.name]
		if !ok {
			return tracker.ErrMemberNotFound
		}
		if m.role == tracker.RolePrimary {
			return tracker.ErrWrongState
		}
		delete(n.members, a.name)
		for i, nm := range n.order {
			if nm == a.name {
				n.order = append(n.order[:i], n.order[i+1:]...)
				break
			}
		}
		n.recompute()
		return nil
	case "promote":
		if !validName(a.name) {
			return tracker.ErrInvalidArg
		}
		cand, ok := n.members[a.name]
		if !ok {
			return tracker.ErrMemberNotFound
		}
		if cand.role != tracker.RoleSync {
			return tracker.ErrWrongState
		}
		g := n.gcp
		mMax := n.maxProc(cand)
		fill := make([]int, 0)
		for seq := 1; seq <= mMax; seq++ {
			if !cand.proc[seq] {
				fill = append(fill, seq)
			}
		}
		lost := append([]int(nil), fill...)
		for seq := mMax + 1; seq <= n.maxSeq; seq++ {
			lost = append(lost, seq)
		}

		old := n.primary
		oldTerm := n.term
		n.term++
		for _, seq := range fill {
			n.ops[seq] = nOp{term: n.term, noop: true}
		}
		for seq := mMax + 1; seq <= n.maxSeq; seq++ {
			delete(n.ops, seq)
		}
		n.maxSeq = mMax
		delete(n.members, old)
		for i, nm := range n.order {
			if nm == old {
				n.order = append(n.order[:i], n.order[i+1:]...)
				break
			}
		}
		for _, nm := range n.order {
			m := n.members[nm]
			if m.role == tracker.RoleCatchup {
				for seq := range m.proc {
					if seq > g {
						delete(m.proc, seq)
					}
				}
				continue
			}
			for seq := range m.proc {
				if seq > g {
					delete(m.proc, seq)
				}
			}
			for seq := g + 1; seq <= mMax; seq++ {
				m.proc[seq] = true
			}
		}
		cand.role = tracker.RolePrimary
		n.primary = a.name
		n.lost = append(n.lost, lost...)
		for _, seq := range lost {
			n.lostVersions[[2]int{seq, oldTerm}] = true
		}
		n.recompute()
		return nil
	}
	return fmt.Errorf("unknown action %s", a.kind)
}

func (n *naive) history(name string) []tracker.Op {
	m := n.members[name]
	seqs := make([]int, 0)
	for seq := range m.proc {
		if _, ok := n.ops[seq]; ok {
			seqs = append(seqs, seq)
		}
	}
	sort.Ints(seqs)
	out := make([]tracker.Op, 0, len(seqs))
	for _, seq := range seqs {
		op := n.ops[seq]
		out = append(out, tracker.Op{Seq: seq, Term: op.term, Body: op.body, Noop: op.noop})
	}
	return out
}

// ---------- 随机序列与对拍 ----------

type snapshot struct {
	term      int
	gcp       int
	maxSeq    int
	primary   string
	lcp       map[string]int
	role      map[string]tracker.Role
	confirmed []int
	lost      []int
	history   map[string][]tracker.Op
}

func takeNaive(n *naive) snapshot {
	s := snapshot{
		term:    n.term,
		gcp:     n.gcp,
		maxSeq:  n.maxSeq,
		primary: n.primary,
		lcp:     map[string]int{},
		role:    map[string]tracker.Role{},
		history: map[string][]tracker.Op{},
	}
	s.confirmed = append(s.confirmed, n.confirmed...)
	s.lost = append(s.lost, n.lost...)
	for _, name := range n.order {
		m := n.members[name]
		s.lcp[name] = n.lcp(m)
		s.role[name] = m.role
		s.history[name] = n.history(name)
	}
	return s
}

func takeReal(g *tracker.Group) snapshot {
	v := g.View()
	s := snapshot{
		term:      v.Term,
		gcp:       v.GCP,
		maxSeq:    v.MaxSeq,
		primary:   v.Primary,
		lcp:       map[string]int{},
		role:      map[string]tracker.Role{},
		history:   map[string][]tracker.Op{},
		confirmed: g.Confirmed(),
		lost:      g.Lost(),
	}
	for _, m := range v.Members {
		s.lcp[m.Name] = m.LCP
		s.role[m.Name] = m.Role
		h, err := g.History(m.Name)
		if err != nil {
			panic(err)
		}
		s.history[m.Name] = h
	}
	return s
}

func sameSnapshot(a, b snapshot) (string, bool) {
	if a.term != b.term {
		return fmt.Sprintf("term %d != %d", a.term, b.term), false
	}
	if a.gcp != b.gcp {
		return fmt.Sprintf("gcp %d != %d", a.gcp, b.gcp), false
	}
	if a.maxSeq != b.maxSeq {
		return fmt.Sprintf("maxSeq %d != %d", a.maxSeq, b.maxSeq), false
	}
	if a.primary != b.primary {
		return fmt.Sprintf("primary %q != %q", a.primary, b.primary), false
	}
	if !reflect.DeepEqual(a.confirmed, b.confirmed) {
		return fmt.Sprintf("confirmed %v != %v", a.confirmed, b.confirmed), false
	}
	if !reflect.DeepEqual(a.lost, b.lost) {
		return fmt.Sprintf("lost %v != %v", a.lost, b.lost), false
	}
	names := map[string]bool{}
	for k := range a.lcp {
		names[k] = true
	}
	for k := range b.lcp {
		names[k] = true
	}
	for name := range names {
		la, oka := a.lcp[name]
		lb, okb := b.lcp[name]
		if oka != okb || la != lb {
			return fmt.Sprintf("lcp[%s] (%d,%v) != (%d,%v)", name, la, oka, lb, okb), false
		}
		ra, rka := a.role[name]
		rb, rkb := b.role[name]
		if rka != rkb || ra != rb {
			return fmt.Sprintf("role[%s] (%d,%v) != (%d,%v)", name, ra, rka, rb, rkb), false
		}
		if !reflect.DeepEqual(a.history[name], b.history[name]) {
			return fmt.Sprintf("history[%s]\n naive=%+v\n real =%+v",
				name, a.history[name], b.history[name]), false
		}
	}
	return "", true
}

func errKey(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, tracker.ErrInvalidArg):
		return "invalid"
	case errors.Is(err, tracker.ErrMemberNotFound):
		return "no-member"
	case errors.Is(err, tracker.ErrMemberExists):
		return "exists"
	case errors.Is(err, tracker.ErrStaleTerm):
		return "stale"
	case errors.Is(err, tracker.ErrWrongState):
		return "state"
	case errors.Is(err, tracker.ErrNotCaughtUp):
		return "behind"
	case errors.Is(err, tracker.ErrPlanStale):
		return "plan-stale"
	default:
		return err.Error()
	}
}

func realExec(g *tracker.Group, a action) error {
	switch a.kind {
	case "write":
		_, err := g.Write(a.body)
		return err
	case "ack":
		return g.Ack(a.name, a.seq, a.term)
	case "add":
		return g.AddReplica(a.name)
	case "sync":
		return g.MarkInSync(a.name)
	case "fail":
		return g.FailReplica(a.name)
	case "promote":
		_, err := promote.Promote(g, a.name)
		return err
	}
	return fmt.Errorf("unknown action %s", a.kind)
}

const candidateNames = "ABCDEFGH"

func genActions(rng *rand.Rand, nMembers int) []action {
	names := make([]string, nMembers)
	for i := range names {
		names[i] = string(candidateNames[i])
	}

	actions := make([]action, 0, 60)
	nextExtra := nMembers
	writes := 0

	pickExisting := func() string {
		return names[rng.Intn(len(names))]
	}

	for step := 0; step < 60; step++ {
		r := rng.Float64()
		switch {
		case r < 0.28:
			writes++
			actions = append(actions, action{kind: "write", body: fmt.Sprintf("b%d", writes)})
		case r < 0.62:
			if writes == 0 {
				continue
			}
			// 有时制造非法参数：seq 越界 / term 越界 / 空名字。
			bad := rng.Intn(12)
			a := action{kind: "ack", name: pickExisting(), seq: 1 + rng.Intn(writes), term: 1}
			if bad == 0 {
				a.seq = writes + 1 + rng.Intn(3)
			} else if bad == 1 {
				a.name = ""
			} else if bad == 2 {
				a.name = "Z"
			}
			actions = append(actions, a)
		case r < 0.72:
			if nextExtra >= len(candidateNames) {
				continue
			}
			name := string(candidateNames[nextExtra])
			nextExtra++
			if rng.Intn(8) == 0 {
				name = "" // 非法名字
			}
			actions = append(actions, action{kind: "add", name: name})
			if name != "" {
				names = append(names, name)
			}
		case r < 0.82:
			actions = append(actions, action{kind: "sync", name: pickExisting()})
		case r < 0.92:
			actions = append(actions, action{kind: "fail", name: pickExisting()})
		default:
			actions = append(actions, action{kind: "promote", name: pickExisting()})
		}
	}
	return actions
}

// TestRandomAgainstNaive 1500 组随机操作序列与逐步朴素模拟对照。
// go test -v 会打印每组输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	if !testing.Verbose() {
		t.Log("re-run with -v for per-sequence inputs, outputs and verdicts")
	}
	for trial := 0; trial < 1500; trial++ {
		seed := int64(trial*7919 + 17)
		r := rand.New(rand.NewSource(seed))
		n := 2 + r.Intn(3)
		actions := genActions(r, n)
		prim := string(candidateNames[0])
		reps := make([]string, n-1)
		for i := range reps {
			reps[i] = string(candidateNames[i+1])
		}
		g, gerr := tracker.New(prim, reps)
		nv := newNaive(prim, reps)
		if gerr != nil {
			t.Fatalf("trial %d: New: %v", trial, gerr)
		}

		var log strings.Builder
		fmt.Fprintf(&log, "--- trial %d seed=%d group=%v+%s\n", trial, seed, prim, reps)
		failed := false
		prevGCP := 0
		for step, a := range actions {
			realErr := realExec(g, a)
			naiveErr := nv.exec(a)
			input := fmt.Sprintf("step %2d %-7s", step, a.kind)
			switch a.kind {
			case "write":
				input += fmt.Sprintf(" body=%q", a.body)
			case "ack":
				input += fmt.Sprintf(" member=%s seq=%d term=%d", dash(a.name), a.seq, a.term)
			default:
				input += fmt.Sprintf(" member=%s", dash(a.name))
			}
			verdict := "OK"
			if ek1, ek2 := errKey(realErr), errKey(naiveErr); ek1 != ek2 {
				verdict = fmt.Sprintf("OUTPUT MISMATCH real=%s naive=%s", ek1, ek2)
				failed = true
			}
			fmt.Fprintf(&log, "%s => real=%-10s naive=%-10s %s\n",
				input, errKey(realErr), errKey(naiveErr), verdict)

			if failed {
				break
			}
			rs, ns := takeReal(g), takeNaive(nv)
			if rs.gcp < prevGCP {
				fmt.Fprintf(&log, "INVARIANT VIOLATION: gcp decreased %d -> %d\n", prevGCP, rs.gcp)
				failed = true
				break
			}
			prevGCP = rs.gcp
			if why, ok := invariants(g, rs, nv.lostVersions); !ok {
				fmt.Fprintf(&log, "INVARIANT VIOLATION: %s\n", why)
				failed = true
				break
			}
			if why, ok := sameSnapshot(ns, rs); !ok {
				fmt.Fprintf(&log, "STATE MISMATCH: %s\n", why)
				fmt.Fprintf(&log, "naive: %+v\nreal : %+v\n", ns, rs)
				failed = true
				break
			}
		}
		if !failed {
			fmt.Fprintf(&log, "VERDICT: all %d steps equivalent\n", len(actions))
		}
		if testing.Verbose() || failed {
			t.Log("\n" + log.String())
		}
		if failed {
			t.Fatalf("trial %d diverged; see logged inputs/outputs above", trial)
		}
	}
}

func dash(s string) string {
	if s == "" {
		return `""`
	}
	return s
}

// invariants 校验题目列出的全局不变量（gcp 单调性由调用方逐快照相间比较，
// 见测试主循环；这里校验集合/历史类不变量）。
func invariants(g *tracker.Group, s snapshot, lostVersions map[[2]int]bool) (string, bool) {
	v := g.View()
	termOf := map[int]int{}
	for _, op := range v.Ops {
		termOf[op.Seq] = op.Term
	}
	for _, seq := range s.confirmed {
		if lostVersions[[2]int{seq, termOf[seq]}] {
			return fmt.Sprintf("op (seq=%d,term=%d) in both Confirmed %v and Lost %v",
				seq, termOf[seq], s.confirmed, s.lost), false
		}
	}
	// seq <= gcp 的真实操作都在 Confirmed 中（noop 除外）。
	for seq := 1; seq <= s.gcp; seq++ {
		var op tracker.Op
		found := false
		for _, o := range v.Ops {
			if o.Seq == seq {
				op, found = o, true
				break
			}
		}
		if !found || op.Noop {
			continue
		}
		confirmed := false
		for _, c := range s.confirmed {
			if c == seq {
				confirmed = true
				break
			}
		}
		if !confirmed {
			return fmt.Sprintf("real op seq %d <= gcp %d but not confirmed", seq, s.gcp), false
		}
	}
	// 同步集合内任意两成员历史在 seq <= gcp 部分逐条相同。
	var syncNames []string
	for name, role := range s.role {
		if role == tracker.RolePrimary || role == tracker.RoleSync {
			syncNames = append(syncNames, name)
		}
	}
	sort.Strings(syncNames)
	prefix := func(name string) []tracker.Op {
		out := make([]tracker.Op, 0)
		for _, op := range s.history[name] {
			if op.Seq <= s.gcp {
				out = append(out, op)
			}
		}
		return out
	}
	if len(syncNames) > 0 {
		base := prefix(syncNames[0])
		for _, name := range syncNames[1:] {
			if !reflect.DeepEqual(base, prefix(name)) {
				return fmt.Sprintf("sync histories differ below gcp: %s=%+v %s=%+v",
					syncNames[0], base, name, prefix(name)), false
			}
		}
	}
	return "", true
}
