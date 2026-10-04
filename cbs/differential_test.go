package cbs

import (
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naiveLedger 是按规格书写的逐步朴素模拟器，用于对拍：
// Run 拆成逐单位执行，每单位后重判 q 是否归零；零松弛与带宽求和
// 均用 big.Int / big.Rat 直接翻译判定式。
type naiveLedger struct {
	clock   int64
	servers map[string]*naiveServer
}

type naiveServer struct {
	id    string
	Q, P  int64
	q     int64
	d     *big.Int
	w     int64
	woken bool
}

func newNaive() *naiveLedger {
	return &naiveLedger{servers: map[string]*naiveServer{}}
}

func (n *naiveLedger) stateAt(s *naiveServer, now int64) State {
	if s.w > 0 {
		return Ready
	}
	if !s.woken {
		return Released
	}
	lhs := new(big.Int).Mul(big.NewInt(s.q), big.NewInt(s.P))
	rhs := new(big.Int).Mul(new(big.Int).Sub(s.d, big.NewInt(now)), big.NewInt(s.Q))
	if lhs.Cmp(rhs) >= 0 {
		return Released
	}
	return Idle
}

func (n *naiveLedger) occupiedTotal(now int64, skip string) *big.Rat {
	total := new(big.Rat)
	for _, s := range n.servers {
		if s.id == skip {
			continue
		}
		if n.stateAt(s, now) != Released {
			total.Add(total, new(big.Rat).SetFrac64(s.Q, s.P))
		}
	}
	return total
}

func (n *naiveLedger) add(id string, Q, P int64) ErrKind {
	if id == "" || Q < 1 || P < Q || P > MaxQP {
		return ErrInvalidParam
	}
	if _, ok := n.servers[id]; ok {
		return ErrDuplicate
	}
	if len(n.servers) >= MaxServers {
		return ErrCapacityFull
	}
	n.servers[id] = &naiveServer{id: id, Q: Q, P: P, q: Q, d: new(big.Int)}
	return -1
}

func (n *naiveLedger) wake(id string, now, work int64) ErrKind {
	if id == "" || work < 1 || work > MaxWork || now < 0 || now > MaxBacklog {
		return ErrInvalidParam
	}
	s, ok := n.servers[id]
	if !ok {
		return ErrNotFound
	}
	if now < n.clock {
		return ErrClockRewind
	}
	switch n.stateAt(s, now) {
	case Ready:
		if s.w+work > MaxBacklog {
			return ErrInvalidParam
		}
		s.w += work
	case Idle:
		s.w = work
	case Released:
		total := n.occupiedTotal(now, "")
		total.Add(total, new(big.Rat).SetFrac64(s.Q, s.P))
		if total.Cmp(big.NewRat(1, 1)) > 0 {
			return ErrBandwidth
		}
		s.q = s.Q
		s.d.SetInt64(now + s.P)
		s.w = work
	}
	s.woken = true
	n.clock = now
	return -1
}

func (n *naiveLedger) earliestReady() *naiveServer {
	var best *naiveServer
	for _, s := range n.servers {
		if s.w == 0 {
			continue
		}
		if best == nil || s.d.Cmp(best.d) < 0 ||
			(s.d.Cmp(best.d) == 0 && s.id < best.id) {
			best = s
		}
	}
	return best
}

func (n *naiveLedger) run(id string, now, delta int64) ErrKind {
	if id == "" || delta < 1 || now < 0 || now > MaxBacklog {
		return ErrInvalidParam
	}
	s, ok := n.servers[id]
	if !ok {
		return ErrNotFound
	}
	if now < n.clock {
		return ErrClockRewind
	}
	if s.w == 0 || delta > s.w {
		return ErrNotRunnable
	}
	if e := n.earliestReady(); e != s {
		return ErrNotEarliest
	}
	// 逐单位运行，每单位后重判 q 是否归零。
	for i := int64(0); i < delta; i++ {
		s.q--
		if s.q == 0 {
			s.q = s.Q
			s.d.Add(s.d, big.NewInt(s.P))
		}
		s.w--
	}
	n.clock = now + delta
	return -1
}

func (n *naiveLedger) remove(id string) ErrKind {
	if id == "" {
		return ErrInvalidParam
	}
	s, ok := n.servers[id]
	if !ok {
		return ErrNotFound
	}
	if n.stateAt(s, n.clock) != Released {
		return ErrBusy
	}
	delete(n.servers, id)
	return -1
}

// naiveDump 与 dump 输出完全相同的格式。
func naiveDump(n *naiveLedger) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "clock=%d total=%s", n.clock, n.occupiedTotal(n.clock, "").RatString())
	if s := n.earliestReady(); s != nil {
		fmt.Fprintf(&sb, " next=%s", s.id)
	} else {
		sb.WriteString(" next=<none>")
	}
	var ids []string
	for id := range n.servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := n.servers[id]
		fmt.Fprintf(&sb, " | %s %s q=%d d=%s w=%d",
			id, n.stateAt(s, n.clock), s.q, s.d, s.w)
	}
	return sb.String()
}

// rationale 给出当前时钟下每台服务器状态判定的依据（零松弛判定式两侧的值）。
func rationale(n *naiveLedger) string {
	var ids []string
	for id := range n.servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		s := n.servers[id]
		st := n.stateAt(s, n.clock)
		if s.w == 0 && s.woken {
			lhs := new(big.Int).Mul(big.NewInt(s.q), big.NewInt(s.P))
			rhs := new(big.Int).Mul(new(big.Int).Sub(s.d, big.NewInt(n.clock)), big.NewInt(s.Q))
			parts = append(parts, fmt.Sprintf("%s: q·P=%s %s (d−now)·Q=%s → %s",
				id, lhs, cmpSymbol(lhs, rhs), rhs, st))
		} else {
			parts = append(parts, fmt.Sprintf("%s: w=%d woken=%v → %s", id, s.w, s.woken, st))
		}
	}
	return strings.Join(parts, "; ")
}

func cmpSymbol(a, b *big.Int) string {
	switch a.Cmp(b) {
	case -1:
		return "<"
	case 1:
		return ">"
	}
	return "="
}

// checkInvariants 校验规格要求的全时不变量。
func checkInvariants(t *testing.T, l *Ledger) {
	t.Helper()
	if total := l.Total(); total.Cmp(big.NewRat(1, 1)) > 0 {
		t.Fatalf("不变量违反：占用总带宽 %s 超过 1", total.RatString())
	}
	clock := l.Clock()
	l.mu.Lock()
	ids := make([]string, 0, len(l.servers))
	for id := range l.servers {
		ids = append(ids, id)
	}
	l.mu.Unlock()
	for _, id := range ids {
		v, err := l.State(id)
		if err != nil {
			t.Fatalf("State(%s): %v", id, err)
		}
		if v.State != Released && (v.Budget < 1 || v.Budget > v.Q) {
			t.Fatalf("不变量违反：%s 状态 %s 但 q=%d 不在 [1,%d]", id, v.State, v.Budget, v.Q)
		}
		if v.State == Idle && v.Deadline.Cmp(big.NewInt(clock)) <= 0 {
			t.Fatalf("不变量违反：%s 空闲占用但 d=%s ≤ now=%d", id, v.Deadline, clock)
		}
	}
}

// op 是一条随机生成的操作。
type op struct {
	desc  string
	apply func(l *Ledger) error
	naive func(n *naiveLedger) ErrKind
}

// genScript 生成一条确定性的随机操作序列（含 Add 前置）。
// 生成过程中用一个临时朴素账本推演时钟，使 now 大致单调。
func genScript(r *rand.Rand, ids []string, params [][2]int64) []op {
	var ops []op
	sim := newNaive()

	// 预注册随机子集，让后续操作大多落在已存在的服务器上。
	for i, id := range ids {
		if r.Intn(10) < 7 {
			Q, P := params[i][0], params[i][1]
			o := op{
				desc:  fmt.Sprintf("Add(%s,%d,%d)", id, Q, P),
				apply: func(l *Ledger) error { return l.Add(id, Q, P) },
				naive: func(n *naiveLedger) ErrKind { return n.add(id, Q, P) },
			}
			ops = append(ops, o)
			o.naive(sim)
		}
	}

	now := func() int64 {
		if sim.clock > 0 && r.Intn(40) == 0 {
			return sim.clock - 1 // 偶发时钟回退
		}
		return sim.clock + int64(r.Intn(3))
	}
	pickID := func() string {
		if r.Intn(30) == 0 {
			return "zz" // 偶发不存在的编号
		}
		return ids[r.Intn(len(ids))]
	}

	nOps := 40 + r.Intn(60)
	for i := 0; i < nOps; i++ {
		var o op
		switch r.Intn(100) {
		case 0, 1, 2, 3, 4: // Add
			idx := r.Intn(len(ids))
			id, pr := ids[idx], params[idx]
			Q, P := pr[0], pr[1]
			o = op{
				desc:  fmt.Sprintf("Add(%s,%d,%d)", id, Q, P),
				apply: func(l *Ledger) error { return l.Add(id, Q, P) },
				naive: func(n *naiveLedger) ErrKind { return n.add(id, Q, P) },
			}
		case 5, 6, 7, 8, 9, 10, 11, 12, 13, 14: // Remove
			id := pickID()
			o = op{
				desc:  fmt.Sprintf("Remove(%s)", id),
				apply: func(l *Ledger) error { return l.Remove(id) },
				naive: func(n *naiveLedger) ErrKind { return n.remove(id) },
			}
		case 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29,
			30, 31, 32, 33, 34: // Run
			id := pickID()
			nw := now()
			delta := int64(1 + r.Intn(40))
			o = op{
				desc:  fmt.Sprintf("Run(%s,%d,%d)", id, nw, delta),
				apply: func(l *Ledger) error { return l.Run(id, nw, delta) },
				naive: func(n *naiveLedger) ErrKind { return n.run(id, nw, delta) },
			}
		default: // Wake（约 51%）
			id := pickID()
			nw := now()
			work := int64(1 + r.Intn(30))
			o = op{
				desc:  fmt.Sprintf("Wake(%s,%d,%d)", id, nw, work),
				apply: func(l *Ledger) error { return l.Wake(id, nw, work) },
				naive: func(n *naiveLedger) ErrKind { return n.wake(id, nw, work) },
			}
		}
		ops = append(ops, o)
		o.naive(sim) // 推演时钟（结果不影响生成）
	}
	return ops
}

// 对拍：2000 组随机操作序列，逐操作比较主实现与朴素模拟器的
// 拒绝点与全部 q、d、w，并校验全时不变量；日志打印输入、输出与判定依据。
func TestDifferentialRandom(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		r := rand.New(rand.NewSource(int64(seq)*7919 + 42))
		nIDs := 2 + r.Intn(5)
		ids := make([]string, nIDs)
		params := make([][2]int64, nIDs)
		for i := range ids {
			ids[i] = string(rune('a' + i))
			P := int64(1 + r.Intn(8))
			if r.Intn(20) == 0 {
				P = int64(1 + r.Intn(MaxQP))
			}
			Q := int64(1 + r.Intn(int(P)))
			params[i] = [2]int64{Q, P}
		}
		script := genScript(r, ids, params)

		main := NewLedger()
		replay := NewLedger() // 同一序列重放，验证完全相同的拒绝点与状态
		naive := newNaive()
		for i, o := range script {
			mainErr := o.apply(main)
			replayErr := o.apply(replay)
			naiveKind := o.naive(naive)

			mainKind := errKind(mainErr)
			if mainErr == nil {
				mainKind = -1
			}
			if mainKind != naiveKind {
				t.Fatalf("seq=%d op#%d %s：拒绝点不一致，主实现=%v 朴素=%v",
					seq, i, o.desc, mainErr, kindName(naiveKind))
			}
			if (replayErr == nil) != (mainErr == nil) ||
				(mainErr != nil && errKind(replayErr) != mainKind) {
				t.Fatalf("seq=%d op#%d %s：重放拒绝点不一致", seq, i, o.desc)
			}
			dMain, dNaive, dReplay := dump(main), naiveDump(naive), dump(replay)
			if dMain != dNaive {
				t.Fatalf("seq=%d op#%d %s：状态不一致\n 主实现: %s\n 朴素:   %s",
					seq, i, o.desc, dMain, dNaive)
			}
			if dMain != dReplay {
				t.Fatalf("seq=%d op#%d %s：重放状态不一致\n 第一次: %s\n 第二次: %s",
					seq, i, o.desc, dMain, dReplay)
			}
			checkInvariants(t, main)
			t.Logf("seq=%d op#%d 输入=%s 输出=%s 判定依据: %s",
				seq, i, o.desc, resultName(mainErr), rationale(naive))
		}
	}
}

func kindName(k ErrKind) string {
	if k == -1 {
		return "成功"
	}
	return k.String()
}

func resultName(err error) string {
	if err == nil {
		return "成功"
	}
	return errKind(err).String()
}
