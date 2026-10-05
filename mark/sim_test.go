package mark_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"ontology/backfill"
	"ontology/mark"
)

// 朴素模拟：逐分区扫描求 W、逐作业扫描求相交与占用，与优化实现对照。

type njob struct {
	name           string
	a, b, ttl, ddl int
	staged         map[int]bool
}

type naive struct {
	cap    int
	vers   map[int]int
	jobs   map[string]*njob // 含已过期未落地
	acked  map[string]int
	maxNow int
}

func newNaive(capacity int) *naive {
	return &naive{cap: capacity, vers: map[int]int{}, jobs: map[string]*njob{}, acked: map[string]int{}}
}

func nValidNow(now int) bool { return 0 <= now && now <= mark.MaxNow }

func nValidPart(p int) bool { return 0 <= p && p <= mark.MaxPart }

func (n *naive) liveJobs(now int) []*njob {
	var out []*njob
	for _, j := range n.jobs {
		if j.ddl > now {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(x, y int) bool { return out[x].a < out[y].a })
	return out
}

func (n *naive) sweep(now int) {
	for name, j := range n.jobs {
		if j.ddl <= now {
			delete(n.jobs, name)
		}
	}
}

func (n *naive) W() int {
	w := -1
	for n.vers[w+1] >= 1 {
		w++
	}
	return w
}

// S 只反映已落地状态（含已过期未取消的作业）。
func (n *naive) S() int {
	w := n.W()
	amin, ok := 0, false
	for _, j := range n.jobs {
		if !ok || j.a < amin {
			amin, ok = j.a, true
		}
	}
	if ok && amin-1 < w {
		return amin - 1
	}
	return w
}

// stableLive 按 now 处的活跃作业计算稳定水位（Ack 判定用）。
func (n *naive) stableLive(now int) int {
	w := n.W()
	live := n.liveJobs(now)
	if len(live) > 0 && live[0].a-1 < w {
		return live[0].a - 1
	}
	return w
}

func (n *naive) ackedOf(c string) int {
	if v, ok := n.acked[c]; ok {
		return v
	}
	return -1
}

func (n *naive) getJob(name string, now int) *njob {
	j := n.jobs[name]
	if j == nil || j.ddl <= now {
		return nil
	}
	return j
}

func (n *naive) commit(p, now int) error {
	if !nValidPart(p) || !nValidNow(now) {
		return mark.ErrInvalid
	}
	if now < n.maxNow {
		return mark.ErrClockRegress
	}
	for _, j := range n.liveJobs(now) {
		if j.a <= p && p <= j.b {
			return &backfill.Error{Kind: mark.ErrHeld, Job: j.name, Part: p}
		}
	}
	if n.vers[p] >= 1 {
		return mark.ErrAlready
	}
	n.vers[p] = 1
	n.sweep(now)
	n.maxNow = now
	return nil
}

func (n *naive) begin(name string, a, b, ttl, now int) error {
	if !nValidNow(now) || a < 0 || a > b || b > mark.MaxPart ||
		b-a+1 > mark.MaxSpan || ttl < 1 || ttl > mark.MaxTTL {
		return mark.ErrInvalid
	}
	if now < n.maxNow {
		return mark.ErrClockRegress
	}
	live := n.liveJobs(now)
	if j := n.jobs[name]; j != nil && j.ddl > now {
		return &backfill.Error{Kind: mark.ErrJobExists, Job: name}
	}
	if len(live) >= n.cap {
		return &backfill.Error{Kind: mark.ErrTooManyJobs, Job: name}
	}
	for _, j := range live { // 按起点升序，第一个相交者即起点最小冲突者
		if j.a <= b && a <= j.b {
			return &backfill.Error{Kind: mark.ErrOverlap, Job: j.name}
		}
	}
	n.sweep(now)
	n.jobs[name] = &njob{name: name, a: a, b: b, ttl: ttl, ddl: now + ttl, staged: map[int]bool{}}
	n.maxNow = now
	return nil
}

func (n *naive) stage(name string, p, now int) error {
	if !nValidPart(p) || !nValidNow(now) {
		return mark.ErrInvalid
	}
	if now < n.maxNow {
		return mark.ErrClockRegress
	}
	j := n.getJob(name, now)
	if j == nil {
		return &backfill.Error{Kind: mark.ErrNoJob, Job: name}
	}
	if p < j.a || p > j.b {
		return &backfill.Error{Kind: mark.ErrOutOfRange, Job: name, Part: p}
	}
	n.sweep(now)
	j.staged[p] = true
	n.maxNow = now
	return nil
}

func (n *naive) heartbeat(name string, now int) error {
	if !nValidNow(now) {
		return mark.ErrInvalid
	}
	if now < n.maxNow {
		return mark.ErrClockRegress
	}
	j := n.getJob(name, now)
	if j == nil {
		return &backfill.Error{Kind: mark.ErrNoJob, Job: name}
	}
	n.sweep(now)
	j.ddl = now + j.ttl
	n.maxNow = now
	return nil
}

func (n *naive) finish(name string, now int) ([]mark.Revision, error) {
	if !nValidNow(now) {
		return nil, mark.ErrInvalid
	}
	if now < n.maxNow {
		return nil, mark.ErrClockRegress
	}
	j := n.getJob(name, now)
	if j == nil {
		return nil, &backfill.Error{Kind: mark.ErrNoJob, Job: name}
	}
	for p := j.a; p <= j.b; p++ {
		if !j.staged[p] {
			return nil, &backfill.Error{Kind: mark.ErrIncomplete, Job: name, Part: p}
		}
	}
	n.sweep(now)
	pre := map[int]int{}
	for p := j.a; p <= j.b; p++ {
		pre[p] = n.vers[p]
		n.vers[p]++
	}
	delete(n.jobs, name)
	n.maxNow = now
	consumers := make([]string, 0, len(n.acked))
	for c := range n.acked {
		consumers = append(consumers, c)
	}
	sort.Strings(consumers)
	var revs []mark.Revision
	for _, c := range consumers {
		upto := n.acked[c]
		var ps []int
		for p := j.a; p <= j.b && p <= upto; p++ {
			if pre[p] >= 1 {
				ps = append(ps, p)
			}
		}
		if len(ps) > 0 {
			revs = append(revs, mark.Revision{Consumer: c, Parts: ps})
		}
	}
	return revs, nil
}

func (n *naive) abort(name string, now int) error {
	if !nValidNow(now) {
		return mark.ErrInvalid
	}
	if now < n.maxNow {
		return mark.ErrClockRegress
	}
	j := n.getJob(name, now)
	if j == nil {
		return &backfill.Error{Kind: mark.ErrNoJob, Job: name}
	}
	n.sweep(now)
	delete(n.jobs, name)
	n.maxNow = now
	return nil
}

func (n *naive) ack(consumer string, upto, now int) error {
	if upto < -1 || !nValidNow(now) {
		return mark.ErrInvalid
	}
	if now < n.maxNow {
		return mark.ErrClockRegress
	}
	if upto < n.ackedOf(consumer) {
		return mark.ErrAckRegress
	}
	if upto > n.stableLive(now) {
		return mark.ErrBeyondStable
	}
	n.sweep(now)
	n.acked[consumer] = upto
	n.maxNow = now
	return nil
}

// ---- 对照测试 ----

type rndOp struct {
	kind         string
	name         string
	a, b, ttl    int
	p, upto, now int
}

func applySys(s *mark.System, o rndOp) ([]mark.Revision, error) {
	switch o.kind {
	case "commit":
		return nil, s.Commit(o.p, o.now)
	case "begin":
		return nil, s.Begin(o.name, o.a, o.b, o.ttl, o.now)
	case "stage":
		return nil, s.Stage(o.name, o.p, o.now)
	case "heartbeat":
		return nil, s.Heartbeat(o.name, o.now)
	case "finish":
		return s.Finish(o.name, o.now)
	case "abort":
		return nil, s.Abort(o.name, o.now)
	case "ack":
		return nil, s.Ack(o.name, o.upto, o.now)
	}
	panic("unknown op " + o.kind)
}

func applyNaive(n *naive, o rndOp) ([]mark.Revision, error) {
	switch o.kind {
	case "commit":
		return nil, n.commit(o.p, o.now)
	case "begin":
		return nil, n.begin(o.name, o.a, o.b, o.ttl, o.now)
	case "stage":
		return nil, n.stage(o.name, o.p, o.now)
	case "heartbeat":
		return nil, n.heartbeat(o.name, o.now)
	case "finish":
		return n.finish(o.name, o.now)
	case "abort":
		return nil, n.abort(o.name, o.now)
	case "ack":
		return nil, n.ack(o.name, o.upto, o.now)
	}
	panic("unknown op " + o.kind)
}

var sentinels = []error{
	mark.ErrInvalid, mark.ErrClockRegress,
	mark.ErrNoJob, mark.ErrJobExists, mark.ErrTooManyJobs,
	mark.ErrOverlap, mark.ErrOutOfRange, mark.ErrIncomplete,
	mark.ErrHeld, mark.ErrAlready,
	mark.ErrAckRegress, mark.ErrBeyondStable,
}

func kindOf(err error) error {
	for _, s := range sentinels {
		if errors.Is(err, s) {
			return s
		}
	}
	return nil
}

type outcome struct {
	kind error
	job  string
	part int
	revs []mark.Revision
}

func outcomeOf(err error, revs []mark.Revision) outcome {
	o := outcome{kind: kindOf(err), revs: revs}
	var be *backfill.Error
	if errors.As(err, &be) {
		o.job, o.part = be.Job, be.Part
	}
	return o
}

func sameOutcome(a, b outcome) bool {
	return a.kind == b.kind && a.job == b.job && a.part == b.part &&
		reflect.DeepEqual(a.revs, b.revs)
}

// 生成一个随机操作，可窥探朴素模拟的当前状态以提高命中率。
func genOp(rng *rand.Rand, na *naive, now int) rndOp {
	opNow := now
	if rng.Intn(100) < 4 {
		opNow = now - 1 // 时钟回退；now=0 时为 -1，触发参数非法
	}
	jobName := func() string { return fmt.Sprintf("j%d", rng.Intn(4)) }
	consumer := func() string { return fmt.Sprintf("c%d", rng.Intn(3)) }
	switch x := rng.Intn(100); {
	case x < 22: // 实时提交
		p := rng.Intn(30)
		if rng.Intn(100) < 40 {
			p = na.W() + 1 + rng.Intn(3) // 偏向推进 W
		}
		switch rng.Intn(25) {
		case 0:
			p = -1
		case 1:
			p = mark.MaxPart + 1
		}
		return rndOp{kind: "commit", p: p, now: opNow}
	case x < 37: // 开始回填
		a, ttl := rng.Intn(30), 1+rng.Intn(8)
		b := a + rng.Intn(6)
		switch rng.Intn(20) {
		case 0:
			ttl = 0
		case 1:
			ttl = mark.MaxTTL + 1
		case 2:
			b = a + mark.MaxSpan // 跨度 1001，非法
		case 3:
			a, b = 5, 3
		}
		return rndOp{kind: "begin", name: jobName(), a: a, b: b, ttl: ttl, now: opNow}
	case x < 57: // 暂存
		p := rng.Intn(32) - 1
		if live := na.liveJobs(opNow); len(live) > 0 && rng.Intn(100) < 60 {
			j := live[rng.Intn(len(live))]
			p = j.a + rng.Intn(j.b-j.a+1) // 偏向落在活跃区间内
		}
		return rndOp{kind: "stage", name: jobName(), p: p, now: opNow}
	case x < 62:
		return rndOp{kind: "heartbeat", name: jobName(), now: opNow}
	case x < 77:
		return rndOp{kind: "finish", name: jobName(), now: opNow}
	case x < 82:
		return rndOp{kind: "abort", name: jobName(), now: opNow}
	default: // 确认
		c := consumer()
		last, effS := na.ackedOf(c), na.stableLive(opNow)
		cands := []int{-2, -1, last, last + 1, effS, effS + 1, rng.Intn(30)}
		return rndOp{kind: "ack", name: c, upto: cands[rng.Intn(len(cands))], now: opNow}
	}
}

// 1500 组随机操作序列：优化实现与逐分区扫描的朴素模拟逐步对照，
// 日志打印输入、输出与判定依据；并校验 probes/cmps 上界与 S<=W 等不变量；
// 最后整序列重放一次，验证结果可精确复现。
func TestRandomVsNaive(t *testing.T) {
	const sequences, opsPerSeq, maxP = 1500, 60, 40
	for seq := 0; seq < sequences; seq++ {
		t.Run(fmt.Sprintf("seq%04d", seq), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(seq)))
			sys := mark.New(3)
			na := newNaive(3)
			ops := make([]rndOp, 0, opsPerSeq)
			records := make([]outcome, 0, opsPerSeq)
			now := 0
			for i := 0; i < opsPerSeq; i++ {
				now += rng.Intn(3)
				o := genOp(rng, na, now)
				ops = append(ops, o)

				wBefore, sBefore := sys.W(), sys.S()
				probesBefore, cmpsBefore := sys.Probes(), sys.Cmps()
				revsS, errS := applySys(sys, o)
				revsN, errN := applyNaive(na, o)
				got, want := outcomeOf(errS, revsS), outcomeOf(errN, revsN)
				if !sameOutcome(got, want) {
					t.Fatalf("op %d %+v: got %+v, naive wants %+v", i, o, got, want)
				}
				records = append(records, got)

				// 判定依据：水位、活跃作业、确认值。
				t.Logf("op=%03d %+v -> kind=%v job=%q part=%d revs=%v | W:%d->%d S:%d->%d jobs=%d acked=%v",
					i, o, got.kind, got.job, got.part, got.revs,
					wBefore, sys.W(), sBefore, sys.S(), len(na.jobs), na.acked)

				// 不变量。
				if sys.S() > sys.W() {
					t.Fatalf("op %d: S=%d > W=%d", i, sys.S(), sys.W())
				}
				if o.kind == "commit" || o.kind == "finish" {
					dProbes := sys.Probes() - probesBefore
					if got.kind == nil {
						if bound := int64(sys.W()-wBefore) + 1; dProbes > bound {
							t.Fatalf("op %d: probes delta %d > (W delta)+1 = %d", i, dProbes, bound)
						}
					} else if dProbes != 0 {
						t.Fatalf("op %d: rejected op probed %d partitions", i, dProbes)
					}
				}
				if o.kind == "begin" {
					if d := sys.Cmps() - cmpsBefore; d > 2 {
						t.Fatalf("op %d: cmps delta %d > 2", i, d)
					}
				}
				if o.kind == "ack" && got.kind == nil && o.upto > na.stableLive(o.now) {
					t.Fatalf("op %d: acked %d beyond stable %d at accept time", i, o.upto, na.stableLive(o.now))
				}

				// 状态对照。
				if sys.W() != na.W() || sys.S() != na.S() {
					t.Fatalf("op %d: W/S = %d/%d, naive %d/%d", i, sys.W(), sys.S(), na.W(), na.S())
				}
				for p := 0; p <= maxP; p++ {
					if sys.Ver(p) != na.vers[p] {
						t.Fatalf("op %d: Ver(%d) = %d, naive %d", i, p, sys.Ver(p), na.vers[p])
					}
				}
				for _, c := range []string{"c0", "c1", "c2"} {
					if sys.Acked(c) != na.ackedOf(c) {
						t.Fatalf("op %d: Acked(%s) = %d, naive %d", i, c, sys.Acked(c), na.ackedOf(c))
					}
				}
			}

			// 相同操作序列重放，结果须完全一致。
			replay := mark.New(3)
			for i, o := range ops {
				revs, err := applySys(replay, o)
				if got := outcomeOf(err, revs); !sameOutcome(got, records[i]) {
					t.Fatalf("replay diverged at op %d %+v: got %+v, first run %+v", i, o, got, records[i])
				}
			}
			if replay.W() != sys.W() || replay.S() != sys.S() {
				t.Fatalf("replay W/S = %d/%d, first run %d/%d", replay.W(), replay.S(), sys.W(), sys.S())
			}
			for p := 0; p <= maxP; p++ {
				if replay.Ver(p) != sys.Ver(p) {
					t.Fatalf("replay Ver(%d) = %d, first run %d", p, replay.Ver(p), sys.Ver(p))
				}
			}
		})
	}
}
