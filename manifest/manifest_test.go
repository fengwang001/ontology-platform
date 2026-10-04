package manifest

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/job"
)

func tmplRungs() []Rung {
	return []Rung{
		{Name: "360", Height: 360, Bitrate: 800, Required: true},
		{Name: "720", Height: 720, Bitrate: 2500, Required: true},
		{Name: "1080", Height: 1080, Bitrate: 5000},
		{Name: "2160", Height: 2160, Bitrate: 16000},
	}
}

func newPub(t *testing.T, rungs []Rung, retry, conc int, now int64) *Publisher {
	t.Helper()
	p, err := New(rungs, retry, conc, now)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func names(rs []Rung) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

func rungs3() []Rung {
	return []Rung{
		{Name: "360", Height: 360, Bitrate: 800, Required: true},
		{Name: "720", Height: 720, Bitrate: 2500, Required: true},
		{Name: "1080", Height: 1080, Bitrate: 5000},
	}
}

// TestExample2Lifecycle 复现题目例二（成功路径与终败路径）。
func TestExample2Lifecycle(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		p := newPub(t, rungs3(), 1, 2, 0)
		must(t, p.Submit(1, "j", 1080, 5000))
		if _, _, err := p.Get("j"); !errors.Is(err, ErrNotReady) {
			t.Fatalf("no version yet: %v", err)
		}
		must(t, p.Start(2, "j", "360"))
		must(t, p.Start(2, "j", "1080"))
		if err := p.Start(2, "j", "720"); !errors.Is(err, ErrBusy) {
			t.Fatalf("busy: %v", err)
		}
		must(t, p.Finish(3, "j", "1080", true, 100))
		if _, _, err := p.Get("j"); !errors.Is(err, ErrNotReady) {
			t.Fatalf("higher rung done first, L empty: %v", err)
		}
		must(t, p.Finish(4, "j", "360", true, 50))
		if _, _, err := p.Get("j"); !errors.Is(err, ErrNotReady) {
			t.Fatalf("L={360} missing required 720: %v", err)
		}
		must(t, p.Start(5, "j", "720"))
		must(t, p.Finish(6, "j", "720", false, 0))
		if _, _, err := p.Get("j"); !errors.Is(err, ErrNotReady) {
			t.Fatalf("failed attempt back to pending: %v", err)
		}
		must(t, p.Start(7, "j", "720"))
		must(t, p.Finish(8, "j", "720", true, 70))
		v, m, err := p.Get("j")
		if err != nil || v != 1 {
			t.Fatalf("first version: v=%d err=%v", v, err)
		}
		if got := names(m); len(got) != 3 || got[0] != "360" || got[1] != "720" || got[2] != "1080" {
			t.Fatalf("manifest = %v", got)
		}
		if err := p.Start(9, "j", "360"); !errors.Is(err, ErrTerminated) {
			t.Fatalf("done job start: %v", err)
		}
	})

	t.Run("required_final_fail", func(t *testing.T) {
		p := newPub(t, rungs3(), 1, 2, 0)
		must(t, p.Submit(1, "j", 1080, 5000))
		for _, name := range []string{"360", "1080"} {
			must(t, p.Start(2, "j", name))
		}
		must(t, p.Finish(3, "j", "360", true, 1))
		must(t, p.Start(4, "j", "720"))
		must(t, p.Finish(5, "j", "720", false, 0))
		must(t, p.Start(6, "j", "720"))
		must(t, p.Finish(7, "j", "720", false, 0))
		if _, _, err := p.Get("j"); !errors.Is(err, ErrJobFailed) {
			t.Fatalf("want ErrJobFailed, got %v", err)
		}
		if err := p.Start(8, "j", "720"); !errors.Is(err, ErrTerminated) {
			t.Fatalf("failed job start terminated: %v", err)
		}
		before := len(p.jobs["j"].versions)
		if err := p.Finish(9, "j", "1080", true, 123); err != nil {
			t.Fatalf("running finish accepted after failure: %v", err)
		}
		if after := len(p.jobs["j"].versions); after != before {
			t.Fatalf("no publish after job failed: before=%d after=%d", before, after)
		}
		task := p.jobs["j"].job.Snapshot()[2]
		if task.State != job.Done || task.Size != 123 {
			t.Fatalf("result must still be recorded: %+v", task)
		}
	})
}

// TestOptionalFailureUnblocks 可选档终败后，上方已 Done 档进入 L，增一版。
func TestOptionalFailureUnblocks(t *testing.T) {
	p := newPub(t, tmplRungs(), 0, 4, 0)
	must(t, p.Submit(1, "j", 2160, 20000))
	for _, name := range []string{"360", "720", "2160"} {
		must(t, p.Start(2, "j", name))
	}
	for _, name := range []string{"360", "720", "2160"} {
		must(t, p.Finish(3, "j", name, true, 1))
	}
	v, _, err := p.Get("j")
	if err != nil || v != 1 {
		t.Fatalf("v1 with required prefix: v=%d err=%v", v, err)
	}
	must(t, p.Start(4, "j", "1080"))
	must(t, p.Finish(5, "j", "1080", false, 0))
	v2, m, err := p.Get("j")
	if err != nil || v2 != 2 {
		t.Fatalf("optional fail unblocks 2160 => v2, got v=%d err=%v", v2, err)
	}
	got := names(m)
	if len(got) != 3 || got[2] != "2160" {
		t.Fatalf("manifest after unblock = %v", got)
	}
	if !p.jobs["j"].job.Done() {
		t.Fatal("all terminal, no required failure => done")
	}
	if v3, _, _ := p.Get("j"); v3 != 2 {
		t.Fatalf("unchanged L must not publish: %d", v3)
	}
}

// TestRejectionOrdering 校验拒绝次序与被拒不改状态（含时钟与 attempt）。
func TestRejectionOrdering(t *testing.T) {
	t.Run("submit_order", func(t *testing.T) {
		p := newPub(t, tmplRungs(), 1, 2, 10)
		if err := p.Submit(1, "", 480, 1); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("invalid first: %v", err)
		}
		must(t, p.Submit(10, "j", 1080, 5000))
		if err := p.Submit(9, "j", 360, 5000); !errors.Is(err, ErrClockRewind) {
			t.Fatalf("clock before exists: %v", err)
		}
		if err := p.Submit(11, "j", 360, 5000); !errors.Is(err, ErrJobExists) {
			t.Fatalf("exists before source: %v", err)
		}
		if err := p.Submit(11, "k", 360, 5000); !errors.Is(err, ErrSourceInsufficient) {
			t.Fatalf("source insufficient last: %v", err)
		}
		if p.now != 10 {
			t.Fatalf("rejected ops must not move clock: %d", p.now)
		}
	})

	t.Run("start_order", func(t *testing.T) {
		p := newPub(t, tmplRungs(), 1, 2, 10)
		must(t, p.Submit(10, "j", 1080, 5000))
		if err := p.Start(9, "j", "360"); !errors.Is(err, ErrClockRewind) {
			t.Fatalf("clock before missing job: %v", err)
		}
		if err := p.Start(11, "nope", "360"); !errors.Is(err, ErrJobNotFound) {
			t.Fatalf("missing job: %v", err)
		}
		must(t, p.Start(11, "j", "360"))
		must(t, p.Finish(12, "j", "360", true, 1))
		must(t, p.Start(12, "j", "720"))
		must(t, p.Finish(13, "j", "720", true, 1))
		must(t, p.Start(13, "j", "1080"))
		must(t, p.Finish(14, "j", "1080", true, 1))
		if err := p.Start(15, "j", "ghost"); !errors.Is(err, ErrTerminated) {
			t.Fatalf("terminated before rung check: %v", err)
		}

		q := newPub(t, tmplRungs(), 1, 1, 0)
		must(t, q.Submit(0, "j", 2160, 20000))
		must(t, q.Start(0, "j", "360"))
		if err := q.Start(0, "j", "360"); !errors.Is(err, ErrNotPending) {
			t.Fatalf("state before busy: %v", err)
		}
		if err := q.Start(0, "j", "720"); !errors.Is(err, ErrBusy) {
			t.Fatalf("busy last: %v", err)
		}
		snap := q.jobs["j"].job.Snapshot()[1]
		if snap.State != job.Pending || snap.Attempt != 0 {
			t.Fatalf("rejected start must not change state: %+v", snap)
		}
	})

	t.Run("finish_order", func(t *testing.T) {
		p := newPub(t, tmplRungs(), 1, 2, 10)
		must(t, p.Submit(10, "j", 1080, 5000))
		if err := p.Finish(9, "j", "360", true, 0); !errors.Is(err, ErrClockRewind) {
			t.Fatalf("clock: %v", err)
		}
		if err := p.Finish(11, "nope", "360", true, 0); !errors.Is(err, ErrJobNotFound) {
			t.Fatalf("missing job: %v", err)
		}
		if err := p.Finish(11, "j", "ghost", true, 0); !errors.Is(err, ErrRungNotFound) {
			t.Fatalf("rung before state: %v", err)
		}
		if err := p.Finish(11, "j", "360", true, 0); !errors.Is(err, ErrNotRunning) {
			t.Fatalf("state: %v", err)
		}
		if err := p.Finish(11, "j", "360", true, 1e12+1); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("size invalid first: %v", err)
		}
		if p.now != 10 {
			t.Fatal("rejected finish must not move clock")
		}
	})
}

// TestGetVersionHistory 历史版本、真超集不变量与越界。
func TestGetVersionHistory(t *testing.T) {
	p := newPub(t, tmplRungs(), 0, 4, 0)
	must(t, p.Submit(0, "j", 2160, 20000))
	order := []string{"360", "720", "1080", "2160"}
	for i, name := range order {
		must(t, p.Start(int64(1+i), "j", name))
	}
	for i, name := range order {
		must(t, p.Finish(int64(10+i), "j", name, true, 1))
	}
	// 360 单独 Done 时 L 缺必需档 720 不发版；故共 3 版，长度依次 2/3/4。
	for v := 1; v <= 3; v++ {
		m, err := p.GetVersion("j", v)
		if err != nil {
			t.Fatalf("version %d: %v", v, err)
		}
		if len(m) != v+1 {
			t.Fatalf("version %d len=%d want %d", v, len(m), v+1)
		}
		if v > 1 {
			prev, err := p.GetVersion("j", v-1)
			if err != nil {
				t.Fatal(err)
			}
			if !strictSuperset(m, prev) {
				t.Fatalf("v%d must be strict superset of v%d", v, v-1)
			}
		}
	}
	if _, err := p.GetVersion("j", 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("v0: %v", err)
	}
	if _, err := p.GetVersion("j", 4); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("v4: %v", err)
	}
	if _, err := p.GetVersion("nope", 1); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("unknown job version: %v", err)
	}
	if _, _, err := p.Get("nope"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("unknown job get: %v", err)
	}
}

func strictSuperset(big, small []Rung) bool {
	set := map[string]bool{}
	for _, r := range big {
		set[r.Name] = true
	}
	for _, r := range small {
		if !set[r.Name] {
			return false
		}
	}
	return len(big) > len(small)
}

// TestNewPublisherErrors 构造参数边界。
func TestNewPublisherErrors(t *testing.T) {
	good := tmplRungs()
	cases := []struct {
		rungs []Rung
		retry int
		conc  int
		now   int64
	}{
		{good, -1, 2, 0},
		{good, 6, 2, 0},
		{good, 1, 0, 0},
		{good, 1, 17, 0},
		{good, 1, 2, -1},
		{good, 1, 2, 1e12 + 1},
		{good[:0], 1, 2, 0},
	}
	for i, c := range cases {
		if _, err := New(c.rungs, c.retry, c.conc, c.now); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
}

// ---- 朴素模型：每次需要时从最低档重扫 L，作为随机对照基准 ----

type nstate int

const (
	nPending nstate = iota
	nRunning
	nDone
	nFailed
)

type ntask struct {
	rung    Rung
	state   nstate
	attempt int
	size    int64
}

type njob struct {
	rungs    []Rung
	tasks    []*ntask
	failed   bool
	versions [][]string
}

type naiveEnv struct {
	rungs  []Rung
	maxTry int
	conc   int
	now    int64
	jobs   map[string]*njob
	fin    map[string]int
}

func naiveDerive(template []Rung, srcHeight, srcBitrate int) []Rung {
	cut := []Rung{}
	for _, r := range template {
		if r.Height <= srcHeight {
			cut = append(cut, r)
		}
	}
	for i := range cut {
		if cut[i].Bitrate > srcBitrate {
			cut[i].Bitrate = srcBitrate
		}
	}
	out := []Rung{}
	for _, r := range cut {
		if len(out) == 0 || r.Bitrate > out[len(out)-1].Bitrate {
			out = append(out, r)
		}
	}
	return out
}

func (n *naiveEnv) submit(now int64, id string, h, b int) {
	d := naiveDerive(n.rungs, h, b)
	j := &njob{rungs: d, tasks: make([]*ntask, len(d))}
	for i, r := range d {
		j.tasks[i] = &ntask{rung: r}
	}
	n.jobs[id] = j
	n.fin[id] = 0
	n.now = now
}

func (n *naiveEnv) start(now int64, id, rung string) {
	j := n.jobs[id]
	for _, t := range j.tasks {
		if t.rung.Name == rung {
			t.attempt++
			t.state = nRunning
		}
	}
	n.now = now
}

func (n *naiveEnv) finish(now int64, id, rung string, ok bool, size int64) []string {
	j := n.jobs[id]
	n.fin[id]++
	n.now = now
	for _, t := range j.tasks {
		if t.rung.Name != rung {
			continue
		}
		if ok {
			t.state = nDone
			t.size = size
		} else if t.attempt >= n.maxTry {
			t.state = nFailed
		} else {
			t.state = nPending
		}
	}
	reqFail := false
	for _, t := range j.tasks {
		if t.rung.Required && t.state == nFailed {
			reqFail = true
		}
	}
	if reqFail {
		j.failed = true
		return nil
	}
	l := []string{}
	for _, t := range j.tasks {
		if t.state == nPending || t.state == nRunning {
			break
		}
		if t.state == nDone {
			l = append(l, t.rung.Name)
		}
	}
	have := map[string]bool{}
	for _, x := range l {
		have[x] = true
	}
	for _, r := range j.rungs {
		if r.Required && !have[r.Name] {
			return l
		}
	}
	last := []string{}
	if len(j.versions) > 0 {
		last = j.versions[len(j.versions)-1]
	}
	diff := len(last) != len(l)
	if !diff {
		for i := range l {
			if l[i] != last[i] {
				diff = true
				break
			}
		}
	}
	if diff {
		j.versions = append(j.versions, append([]string{}, l...))
	}
	return l
}

func eqStrings(a, b []string) bool {
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

type opKind int

const (
	opSubmit opKind = iota
	opStart
	opFinish
)

type op struct {
	kind opKind
	now  int64
	id   string
	rung string
	h, b int
	ok   bool
	size int64
}

// precheck 复刻发布器拒绝次序；非空返回串为拒绝原因。
func precheck(n *naiveEnv, o op) string {
	switch o.kind {
	case opSubmit:
		if o.id == "" || o.now < 0 || o.now > 1e12 || o.h < 1 || o.h > 4320 || o.b < 1 || o.b > 1e8 {
			return "invalid-argument"
		}
		if o.now < n.now {
			return "clock-rewind"
		}
		if _, ok := n.jobs[o.id]; ok {
			return "job-exists"
		}
		for _, r := range n.rungs {
			if r.Height > o.h && r.Required {
				return "source-insufficient"
			}
		}
		return ""
	case opStart:
		if o.id == "" || o.rung == "" || o.now < 0 || o.now > 1e12 {
			return "invalid-argument"
		}
		if o.now < n.now {
			return "clock-rewind"
		}
		j, ok := n.jobs[o.id]
		if !ok {
			return "job-not-found"
		}
		if j.failed {
			return "terminated-failed"
		}
		allTerminal := true
		for _, t := range j.tasks {
			if t.state != nDone && t.state != nFailed {
				allTerminal = false
			}
		}
		if allTerminal {
			return "terminated-done"
		}
		var tk *ntask
		for _, x := range j.tasks {
			if x.rung.Name == o.rung {
				tk = x
			}
		}
		if tk == nil {
			return "rung-not-found"
		}
		if tk.state != nPending {
			return "not-pending"
		}
		running := 0
		for _, x := range j.tasks {
			if x.state == nRunning {
				running++
			}
		}
		if running >= n.conc {
			return "busy"
		}
		return ""
	case opFinish:
		if o.id == "" || o.rung == "" || o.now < 0 || o.now > 1e12 || o.size < 0 || o.size > 1e12 {
			return "invalid-argument"
		}
		if o.now < n.now {
			return "clock-rewind"
		}
		j, ok := n.jobs[o.id]
		if !ok {
			return "job-not-found"
		}
		var tk *ntask
		for _, x := range j.tasks {
			if x.rung.Name == o.rung {
				tk = x
			}
		}
		if tk == nil {
			return "rung-not-found"
		}
		if tk.state != nRunning {
			return "not-running"
		}
		return ""
	}
	return ""
}

// TestRandomDifferential 1500 组随机操作序列与朴素重扫模型逐步对照。
func TestRandomDifferential(t *testing.T) {
	const sequences = 1500
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		retry := rng.Intn(6)
		conc := 1 + rng.Intn(4)
		all := tmplRungs()
		nr := 1 + rng.Intn(4)
		templateRungs := append([]Rung{}, all[:nr]...)
		pub, err := New(templateRungs, retry, conc, 0)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		n := &naiveEnv{
			rungs:  append([]Rung{}, templateRungs...),
			maxTry: retry + 1,
			conc:   conc,
			jobs:   map[string]*njob{},
			fin:    map[string]int{},
		}
		var clock int64
		steps := 6 + rng.Intn(20)
		for step := 0; step < steps; step++ {
			o := op{id: "j", now: clock + int64(rng.Intn(3))}
			if _, submitted := n.jobs["j"]; !submitted {
				o.kind = opSubmit
			} else {
				switch rng.Intn(3) {
				case 0:
					o.kind = opSubmit
					o.id = fmt.Sprintf("j%d", step)
				case 1:
					o.kind = opStart
				default:
					o.kind = opFinish
				}
			}
			switch o.kind {
			case opSubmit:
				o.h = templateRungs[0].Height * (1 + rng.Intn(7))
				o.b = 100 + rng.Intn(20000)
			case opStart:
				j0 := n.jobs["j"]
				o.rung = j0.rungs[rng.Intn(len(j0.rungs))].Name
			case opFinish:
				j := n.jobs["j"]
				var running []*ntask
				for _, tk := range j.tasks {
					if tk.state == nRunning {
						running = append(running, tk)
					}
				}
				if len(running) == 0 {
					pend := ""
					for _, tk := range j.tasks {
						if tk.state == nPending {
							pend = tk.rung.Name
							break
						}
					}
					if pend == "" {
						clock = o.now
						continue
					}
					o.kind = opStart
					o.rung = pend
				} else {
					tk := running[rng.Intn(len(running))]
					o.rung = tk.rung.Name
					o.ok = rng.Intn(2) == 1
					if o.ok {
						o.size = int64(rng.Intn(1000))
					}
				}
			}

			reason := precheck(n, o)
			var gotErr error
			switch o.kind {
			case opSubmit:
				gotErr = pub.Submit(o.now, o.id, o.h, o.b)
			case opStart:
				gotErr = pub.Start(o.now, o.id, o.rung)
			case opFinish:
				gotErr = pub.Finish(o.now, o.id, o.rung, o.ok, o.size)
			}
			if (gotErr != nil) != (reason != "") {
				t.Fatalf("seed=%d step=%d op=%+v reason=%q gotErr=%v", seed, step, o, reason, gotErr)
			}
			if gotErr != nil {
				t.Logf("seed=%d step=%d REJECT %+v => %v | %s", seed, step, o, gotErr, reason)
				continue
			}
			basis := ""
			switch o.kind {
			case opSubmit:
				n.submit(o.now, o.id, o.h, o.b)
				basis = fmt.Sprintf("derived=%v", names(naiveDerive(n.rungs, o.h, o.b)))
			case opStart:
				n.start(o.now, o.id, o.rung)
				basis = "attempt+1 -> Running"
			case opFinish:
				l := n.finish(o.now, o.id, o.rung, o.ok, o.size)
				basis = fmt.Sprintf("ok=%v size=%d rescanned L=%v", o.ok, o.size, l)
			}
			clock = o.now
			t.Logf("seed=%d step=%d ACCEPT %+v | %s", seed, step, o, basis)

			for jid, j := range n.jobs {
				e := pub.jobs[jid]
				snap := e.job.Snapshot()
				for i, tk := range j.tasks {
					wantState := map[nstate]job.State{
						nPending: job.Pending, nRunning: job.Running,
						nDone: job.Done, nFailed: job.Failed,
					}[tk.state]
					if snap[i].State != wantState || snap[i].Attempt != tk.attempt {
						t.Fatalf("seed=%d %s rung=%s naive(s=%d,a=%d) impl(s=%d,a=%d)",
							seed, jid, tk.rung.Name, tk.state, tk.attempt, snap[i].State, snap[i].Attempt)
					}
				}
				if e.failed != j.failed {
					t.Fatalf("seed=%d %s failed naive=%v impl=%v", seed, jid, j.failed, e.failed)
				}
				if len(e.versions) != len(j.versions) {
					t.Fatalf("seed=%d %s versions naive=%v impl=%d", seed, jid, j.versions, len(e.versions))
				}
				for vi := range j.versions {
					if got := names(e.versions[vi]); !eqStrings(got, j.versions[vi]) {
						t.Fatalf("seed=%d %s v%d naive=%v impl=%v", seed, jid, vi+1, j.versions[vi], got)
					}
				}
			}
		}

		var bound int64
		for jid, j := range n.jobs {
			bound += int64(len(j.rungs) + n.fin[jid])
		}
		if pub.advance > bound {
			t.Fatalf("seed=%d advance=%d bound=%d", seed, pub.advance, bound)
		}
	}
}

// TestConcurrentOps 并发调用不崩溃，且结果满足关键不变量。
func TestConcurrentOps(t *testing.T) {
	p := newPub(t, tmplRungs(), 2, 16, 0)
	must(t, p.Submit(0, "j", 2160, 20000))
	var wg sync.WaitGroup
	allNames := []string{"360", "720", "1080", "2160"}
	for round := 0; round < 20; round++ {
		for _, name := range allNames {
			wg.Add(2)
			n := name
			go func() {
				defer wg.Done()
				_ = p.Start(int64(100+round), "j", n)
			}()
			go func() {
				defer wg.Done()
				_ = p.Finish(int64(100+round), "j", n, true, 1)
			}()
		}
	}
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = p.Get("j")
		}()
	}
	wg.Wait()
	// 终态校验：Running 档数最终应为 0（每次 Finish 都带了合法 now）。
	e := p.jobs["j"]
	running := 0
	for _, tk := range e.job.Snapshot() {
		if tk.State == job.Running {
			running++
		}
	}
	if running != 0 {
		t.Fatalf("stray running rungs: %d", running)
	}
	if e.failed {
		return
	}
	if v, _, err := p.Get("j"); err != nil || v < 1 {
		t.Fatalf("concurrent completion should publish: v=%d err=%v", v, err)
	}
	// 版本间真超集。
	for v := 2; v <= len(e.versions); v++ {
		if !strictSuperset(e.versions[v-1], e.versions[v-2]) {
			t.Fatalf("v%d not superset of v%d", v, v-1)
		}
	}
}

// TestTouchedIndependentOfJobCount Get/Finish 只触碰 1 条作业记录，与作业总数无关。
func TestTouchedIndependentOfJobCount(t *testing.T) {
	measure := func(n int) int64 {
		p := newPub(t, tmplRungs(), 0, 16, 0)
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("job-%d", i)
			must(t, p.Submit(0, id, 2160, 20000))
			must(t, p.Start(0, id, "360"))
			must(t, p.Start(0, id, "720"))
		}
		before := p.TouchedCount()
		target := fmt.Sprintf("job-%d", n-1)
		must(t, p.Finish(1, target, "360", true, 1))
		must(t, p.Finish(1, target, "720", true, 1))
		_, _, err := p.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		return p.TouchedCount() - before
	}
	c100 := measure(100)
	c10000 := measure(10000)
	t.Logf("touched records: 100 jobs => %d, 10000 jobs => %d", c100, c10000)
	if c100 != 3 || c10000 != 3 {
		t.Fatalf("2 Finish + 1 Get must touch exactly 1 record each, got %d and %d", c100, c10000)
	}
}

// TestAdvanceFrontierMonotonic 前沿只进不退，总检查 ≤ 档数 + Finish 次数。
func TestAdvanceFrontierMonotonic(t *testing.T) {
	p := newPub(t, tmplRungs(), 5, 16, 0)
	must(t, p.Submit(0, "j", 2160, 20000))
	var finishes int64
	// 720 反复失败重试：前沿停在 360 之后不动，advance 不因重试多扫。
	must(t, p.Start(0, "j", "360"))
	must(t, p.Finish(1, "j", "360", true, 1))
	finishes++
	for i := 0; i < 4; i++ {
		must(t, p.Start(int64(2+i*2), "j", "720"))
		must(t, p.Finish(int64(3+i*2), "j", "720", false, 0))
		finishes++
	}
	if p.AdvanceCount() > 4+finishes {
		t.Fatalf("advance=%d bound=%d", p.AdvanceCount(), 4+finishes)
	}
	if got := p.AdvanceCount(); got > 2+finishes {
		t.Fatalf("frontier should stall at index 2: advance=%d", got)
	}
	must(t, p.Start(20, "j", "720"))
	must(t, p.Finish(21, "j", "720", true, 1))
	finishes++
	ts := int64(22)
	for _, name := range []string{"1080", "2160"} {
		must(t, p.Start(ts, "j", name))
		must(t, p.Finish(ts+1, "j", name, true, 1))
		ts += 2
		finishes++
	}
	if want := int64(4) + finishes; p.AdvanceCount() > want {
		t.Fatalf("advance=%d exceeds rungs+finishes=%d", p.AdvanceCount(), want)
	}
	v, m, err := p.Get("j")
	if err != nil || v != 3 || len(m) != 4 {
		t.Fatalf("final manifest: v=%d len=%d err=%v", v, len(m), err)
	}
}
