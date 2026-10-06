package mark_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/backfill"
	"ontology/mark"
	"ontology/part"
)

// naive 是逐分区扫描的朴素参照实现：与优化实现相互独立，语义直接来自规格。
type naive struct {
	vers    map[int]int
	jobs    map[string]*naiveJob
	acks    map[string]int
	maxNow  int
	maxJobs int
}

type naiveJob struct {
	a, b     int
	ttl      int
	deadline int
	staged   map[int]bool
}

func newNaive(maxJobs int) *naive {
	return &naive{
		vers:    make(map[int]int),
		jobs:    make(map[string]*naiveJob),
		acks:    make(map[string]int),
		maxNow:  -1,
		maxJobs: maxJobs,
	}
}

// sweep 落地所有在 now 已过期的作业的取消；只在操作被接受时调用。
func (n *naive) sweep(now int) {
	for name, j := range n.jobs {
		if now >= j.deadline {
			delete(n.jobs, name)
		}
	}
}

// w 逐分区扫描完整水位。
func (n *naive) w() int {
	w := -1
	for {
		if _, ok := n.vers[w+1]; !ok {
			return w
		}
		w++
	}
}

func stableOf(w, amin int, ok bool) int {
	if ok && amin-1 < w {
		return amin - 1
	}
	return w
}

// sLanded 只反映已落地状态（含已过期但取消未落地的作业）。
func (n *naive) sLanded() int {
	amin, ok := 0, false
	for _, j := range n.jobs {
		if !ok || j.a < amin {
			amin, ok = j.a, true
		}
	}
	return stableOf(n.w(), amin, ok)
}

// sAt 反映在 now 的判定视图（过期作业视同不存在）。
func (n *naive) sAt(now int) int {
	amin, ok := 0, false
	for _, j := range n.jobs {
		if now >= j.deadline {
			continue
		}
		if !ok || j.a < amin {
			amin, ok = j.a, true
		}
	}
	return stableOf(n.w(), amin, ok)
}

func (n *naive) alive(name string, now int) (*naiveJob, bool) {
	j, ok := n.jobs[name]
	if !ok || now >= j.deadline {
		return nil, false
	}
	return j, true
}

func validNowArg(now int) bool { return now >= 0 && now <= mark.MaxNow }

func validPartArg(p int) bool { return p >= 0 && p <= part.MaxPart }

func (n *naive) commit(p, now int) error {
	if !validPartArg(p) || !validNowArg(now) {
		return mark.ErrInvalid
	}
	if now < n.maxNow {
		return mark.ErrClock
	}
	for name, j := range n.jobs {
		if now < j.deadline && j.a <= p && p <= j.b {
			return &mark.HeldError{Job: name}
		}
	}
	if _, ok := n.vers[p]; ok {
		return part.ErrAlready
	}
	n.vers[p] = 1
	n.sweep(now)
	n.maxNow = now
	return nil
}

func (n *naive) begin(job string, a, b, ttl, now int) error {
	if job == "" || !validNowArg(now) ||
		a < 0 || a > b || b > part.MaxPart || b-a+1 > backfill.MaxRange ||
		ttl < 1 || ttl > backfill.MaxTTL {
		return mark.ErrInvalid
	}
	if now < n.maxNow {
		return mark.ErrClock
	}
	if _, ok := n.alive(job, now); ok {
		return backfill.ErrJobExists
	}
	cnt := 0
	for _, j := range n.jobs {
		if now < j.deadline {
			cnt++
		}
	}
	if cnt >= n.maxJobs {
		return backfill.ErrTooManyJobs
	}
	// 朴素相交判定：扫描全部活跃作业，取起点最小的冲突者。
	conflict, conflictA := "", 0
	for name, j := range n.jobs {
		if now >= j.deadline {
			continue
		}
		if j.a <= b && a <= j.b && (conflict == "" || j.a < conflictA) {
			conflict, conflictA = name, j.a
		}
	}
	if conflict != "" {
		return &backfill.OverlapError{Conflict: conflict}
	}
	n.sweep(now)
	n.jobs[job] = &naiveJob{a: a, b: b, ttl: ttl, deadline: now + ttl, staged: make(map[int]bool)}
	n.maxNow = now
	return nil
}

func (n *naive) stage(job string, p, now int) error {
	if job == "" || !validPartArg(p) || !validNowArg(now) {
		return mark.ErrInvalid
	}
	if now < n.maxNow {
		return mark.ErrClock
	}
	j, ok := n.alive(job, now)
	if !ok {
		return backfill.ErrNoJob
	}
	if p < j.a || p > j.b {
		return backfill.ErrOutOfRange
	}
	n.sweep(now)
	j.staged[p] = true
	n.maxNow = now
	return nil
}

func (n *naive) heartbeat(job string, now int) error {
	if job == "" || !validNowArg(now) {
		return mark.ErrInvalid
	}
	if now < n.maxNow {
		return mark.ErrClock
	}
	j, ok := n.alive(job, now)
	if !ok {
		return backfill.ErrNoJob
	}
	n.sweep(now)
	j.deadline = now + j.ttl
	n.maxNow = now
	return nil
}

func (n *naive) abort(job string, now int) error {
	if job == "" || !validNowArg(now) {
		return mark.ErrInvalid
	}
	if now < n.maxNow {
		return mark.ErrClock
	}
	if _, ok := n.alive(job, now); !ok {
		return backfill.ErrNoJob
	}
	n.sweep(now)
	delete(n.jobs, job)
	n.maxNow = now
	return nil
}

func (n *naive) finish(job string, now int) (mark.Revisions, error) {
	if job == "" || !validNowArg(now) {
		return nil, mark.ErrInvalid
	}
	if now < n.maxNow {
		return nil, mark.ErrClock
	}
	j, ok := n.alive(job, now)
	if !ok {
		return nil, backfill.ErrNoJob
	}
	for p := j.a; p <= j.b; p++ {
		if !j.staged[p] {
			return nil, &backfill.IncompleteError{Part: p}
		}
	}
	n.sweep(now)
	delete(n.jobs, job)
	// 修订按提交前版本快照计算。
	names := make([]string, 0, len(n.acks))
	for name := range n.acks {
		names = append(names, name)
	}
	sort.Strings(names)
	var rev mark.Revisions
	for _, name := range names {
		upto := n.acks[name]
		var ps []int
		for p := j.a; p <= j.b && p <= upto; p++ {
			if n.vers[p] >= 1 {
				ps = append(ps, p)
			}
		}
		if len(ps) > 0 {
			rev = append(rev, mark.Revision{Consumer: name, Parts: ps})
		}
	}
	for p := j.a; p <= j.b; p++ {
		n.vers[p]++
	}
	n.maxNow = now
	return rev, nil
}

func (n *naive) ack(cons string, upto, now int) error {
	if cons == "" || !validPartArg(upto) || !validNowArg(now) {
		return mark.ErrInvalid
	}
	if now < n.maxNow {
		return mark.ErrClock
	}
	last := -1
	if v, ok := n.acks[cons]; ok {
		last = v
	}
	if upto < last {
		return mark.ErrAckRegress
	}
	if upto > n.sAt(now) {
		return mark.ErrBeyondStable
	}
	n.sweep(now)
	n.acks[cons] = upto
	n.maxNow = now
	return nil
}

// errKey 把错误规范化为可比较的字符串（含携带的作业名/分区号）。
func errKey(err error) string {
	if err == nil {
		return "ok"
	}
	var he *mark.HeldError
	var oe *backfill.OverlapError
	var ie *backfill.IncompleteError
	switch {
	case errors.As(err, &he):
		return "held:" + he.Job
	case errors.As(err, &oe):
		return "overlap:" + oe.Conflict
	case errors.As(err, &ie):
		return fmt.Sprintf("incomplete:%d", ie.Part)
	case errors.Is(err, mark.ErrInvalid):
		return "invalid"
	case errors.Is(err, mark.ErrClock):
		return "clock"
	case errors.Is(err, mark.ErrAckRegress):
		return "ack-regress"
	case errors.Is(err, mark.ErrBeyondStable):
		return "beyond-stable"
	case errors.Is(err, part.ErrAlready):
		return "already"
	case errors.Is(err, backfill.ErrNoJob):
		return "no-job"
	case errors.Is(err, backfill.ErrJobExists):
		return "job-exists"
	case errors.Is(err, backfill.ErrTooManyJobs):
		return "too-many-jobs"
	case errors.Is(err, backfill.ErrOutOfRange):
		return "out-of-range"
	default:
		return "unknown:" + err.Error()
	}
}

var (
	jobNames  = []string{"j0", "j1", "j2", "j3"}
	consNames = []string{"c0", "c1", "c2"}
)

type op struct {
	kind      string
	job, cons string
	p, a, b   int
	ttl, upto int
	now       int
}

func (o op) String() string {
	switch o.kind {
	case "commit":
		return fmt.Sprintf("Commit(p=%d, now=%d)", o.p, o.now)
	case "begin":
		return fmt.Sprintf("Begin(job=%s, a=%d, b=%d, ttl=%d, now=%d)", o.job, o.a, o.b, o.ttl, o.now)
	case "stage":
		return fmt.Sprintf("Stage(job=%s, p=%d, now=%d)", o.job, o.p, o.now)
	case "heartbeat":
		return fmt.Sprintf("Heartbeat(job=%s, now=%d)", o.job, o.now)
	case "abort":
		return fmt.Sprintf("Abort(job=%s, now=%d)", o.job, o.now)
	case "finish":
		return fmt.Sprintf("Finish(job=%s, now=%d)", o.job, o.now)
	case "ack":
		return fmt.Sprintf("Ack(consumer=%s, upto=%d, now=%d)", o.cons, o.upto, o.now)
	}
	return "?"
}

// genOp 生成随机操作；now 大致递增，偶尔回退或大跳以触发时钟拒绝与过期。
func genOp(r *rand.Rand, cur *int) op {
	now := *cur + r.Intn(4) - 1
	if r.Intn(12) == 0 {
		now += 15
	}
	if now < 0 {
		now = 0
	}
	if now > *cur {
		*cur = now
	}
	o := op{now: now}
	switch r.Intn(10) {
	case 0, 1, 2:
		o.kind = "commit"
		o.p = r.Intn(27) - 1 // 偶尔 -1：非法参数
	case 3, 4:
		o.kind = "begin"
		o.job = jobNames[r.Intn(len(jobNames))]
		o.a = r.Intn(26)
		o.b = o.a + r.Intn(6)
		if o.b > 25 {
			o.b = 25
		}
		o.ttl = 1 + r.Intn(8)
	case 5:
		o.kind = "stage"
		o.job = jobNames[r.Intn(len(jobNames))]
		o.p = r.Intn(26)
	case 6:
		o.kind = "heartbeat"
		o.job = jobNames[r.Intn(len(jobNames))]
	case 7:
		o.kind = "finish"
		o.job = jobNames[r.Intn(len(jobNames))]
	case 8:
		o.kind = "abort"
		o.job = jobNames[r.Intn(len(jobNames))]
	default:
		o.kind = "ack"
		o.cons = consNames[r.Intn(len(consNames))]
		o.upto = r.Intn(28) - 1 // 偶尔 -1：非法参数
	}
	return o
}

func applyReal(s *mark.System, o op) (string, mark.Revisions) {
	switch o.kind {
	case "commit":
		return errKey(s.Commit(o.p, o.now)), nil
	case "begin":
		return errKey(s.Begin(o.job, o.a, o.b, o.ttl, o.now)), nil
	case "stage":
		return errKey(s.Stage(o.job, o.p, o.now)), nil
	case "heartbeat":
		return errKey(s.Heartbeat(o.job, o.now)), nil
	case "abort":
		return errKey(s.Abort(o.job, o.now)), nil
	case "finish":
		rev, err := s.Finish(o.job, o.now)
		return errKey(err), rev
	case "ack":
		return errKey(s.Ack(o.cons, o.upto, o.now)), nil
	}
	panic("bad op: " + o.kind)
}

func applyNaive(n *naive, o op) (string, mark.Revisions) {
	switch o.kind {
	case "commit":
		return errKey(n.commit(o.p, o.now)), nil
	case "begin":
		return errKey(n.begin(o.job, o.a, o.b, o.ttl, o.now)), nil
	case "stage":
		return errKey(n.stage(o.job, o.p, o.now)), nil
	case "heartbeat":
		return errKey(n.heartbeat(o.job, o.now)), nil
	case "abort":
		return errKey(n.abort(o.job, o.now)), nil
	case "finish":
		rev, err := n.finish(o.job, o.now)
		return errKey(err), rev
	case "ack":
		return errKey(n.ack(o.cons, o.upto, o.now)), nil
	}
	panic("bad op: " + o.kind)
}

// 1500 组随机操作序列：与逐分区扫描的朴素模拟逐步对照，
// 同时用第二个实例重放验证确定性。日志打印输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	const (
		seqs = 1500
		maxP = 26
	)
	for seed := int64(0); seed < seqs; seed++ {
		r := rand.New(rand.NewSource(seed))
		sys := mark.New(4)
		replay := mark.New(4)
		ref := newNaive(4)
		cur := 0
		ops := 20 + r.Intn(40)
		for i := 0; i < ops; i++ {
			o := genOp(r, &cur)
			gotK, gotRev := applyReal(sys, o)
			repK, repRev := applyReal(replay, o)
			wantK, wantRev := applyNaive(ref, o)
			if gotK != wantK || !reflect.DeepEqual(gotRev, wantRev) {
				t.Fatalf("seed=%d i=%d op=%s\n got: %s rev=%v\nwant: %s rev=%v",
					seed, i, o, gotK, gotRev, wantK, wantRev)
			}
			if repK != gotK || !reflect.DeepEqual(repRev, gotRev) {
				t.Fatalf("seed=%d i=%d op=%s: 重放结果不一致", seed, i, o)
			}
			// 判定依据：水位、每个分区版本、每个消费者确认值都与朴素模拟一致。
			if sys.W() != ref.w() || sys.S() != ref.sLanded() {
				t.Fatalf("seed=%d i=%d op=%s: W/S=%d/%d, want %d/%d",
					seed, i, o, sys.W(), sys.S(), ref.w(), ref.sLanded())
			}
			for p := 0; p <= maxP; p++ {
				if sys.Ver(p) != ref.vers[p] {
					t.Fatalf("seed=%d i=%d op=%s: Ver(%d)=%d, want %d",
						seed, i, o, p, sys.Ver(p), ref.vers[p])
				}
			}
			for _, c := range consNames {
				wantAck := -1
				if v, ok := ref.acks[c]; ok {
					wantAck = v
				}
				if sys.Acked(c) != wantAck {
					t.Fatalf("seed=%d i=%d op=%s: Acked(%s)=%d, want %d",
						seed, i, o, c, sys.Acked(c), wantAck)
				}
			}
			t.Logf("seed=%d i=%d 输入=%s 输出=%s rev=%v W=%d S=%d 判定依据=与朴素模拟逐步对照一致",
				seed, i, o, gotK, gotRev, sys.W(), sys.S())
		}
	}
}

// 并发冒烟：所有操作可并发调用，-race 下验证串行化。
func TestConcurrentSmoke(t *testing.T) {
	sys := mark.New(8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			job := fmt.Sprintf("g%d", g)
			for i := 0; i < 100; i++ {
				p := (g*100 + i) % 300
				_ = sys.Commit(p, i)
				_ = sys.Begin(job, 400+g*4, 400+g*4+3, 50, i)
				_ = sys.Stage(job, 400+g*4, i)
				_ = sys.Heartbeat(job, i)
				_, _ = sys.Finish(job, i)
				_ = sys.Abort(job, i)
				_ = sys.Ack(job, p, i)
				_ = sys.W()
				_ = sys.S()
				_ = sys.Ver(p)
			}
		}(g)
	}
	wg.Wait()
}
