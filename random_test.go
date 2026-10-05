package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/job"
	"ontology/ladder"
	"ontology/manifest"
)

// 朴素模拟：严格按规则逐步实现，清单每次从最低档重扫，与优化实现对照。

type nRung struct {
	name     string
	height   int
	bitrate  int
	required bool
	state    job.State
	attempt  int
	size     int64
}

type nJob struct {
	rungs    []*nRung
	failed   bool
	versions [][]manifest.Entry
}

func (j *nJob) terminated() bool {
	if j.failed {
		return true
	}
	for _, r := range j.rungs {
		if r.state == job.Pending || r.state == job.Running {
			return false
		}
	}
	return true
}

func (j *nJob) find(name string) *nRung {
	for _, r := range j.rungs {
		if r.name == name {
			return r
		}
	}
	return nil
}

type naive struct {
	tmpl   []ladder.Rung
	R, C   int
	maxNow int64
	jobs   map[string]*nJob
}

func newNaive(tmpl []ladder.Rung, r, c int) *naive {
	return &naive{tmpl: tmpl, R: r, C: c, jobs: map[string]*nJob{}}
}

func validNowN(now int64) bool { return 0 <= now && now <= 1_000_000_000_000 }

// derive 三步推导（朴素）。
func (n *naive) derive(srcH, srcB int) ([]*nRung, error) {
	var kept []*nRung
	for _, t := range n.tmpl {
		if t.Height > srcH {
			if t.Required {
				return nil, ladder.ErrSourceInsufficient
			}
			continue
		}
		b := t.Bitrate
		if b > srcB {
			b = srcB
		}
		kept = append(kept, &nRung{name: t.Name, height: t.Height, bitrate: b, required: t.Required})
	}
	var out []*nRung
	for i, r := range kept {
		if i > 0 && r.bitrate <= out[len(out)-1].bitrate {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (n *naive) submit(now int64, id string, srcH, srcB int) error {
	if id == "" || !validNowN(now) || srcH < 1 || srcH > 4320 || srcB < 1 || srcB > 100_000_000 {
		return ErrInvalidParam
	}
	if now < n.maxNow {
		return ErrClockSkew
	}
	if _, ok := n.jobs[id]; ok {
		return ErrJobExists
	}
	rungs, err := n.derive(srcH, srcB)
	if err != nil {
		return err
	}
	n.jobs[id] = &nJob{rungs: rungs}
	n.maxNow = now
	return nil
}

func (n *naive) start(now int64, id, rung string) error {
	if id == "" || rung == "" || !validNowN(now) {
		return ErrInvalidParam
	}
	if now < n.maxNow {
		return ErrClockSkew
	}
	j, ok := n.jobs[id]
	if !ok {
		return ErrJobNotFound
	}
	if j.terminated() {
		return job.ErrTerminated
	}
	r := j.find(rung)
	if r == nil {
		return job.ErrRungNotFound
	}
	if r.state != job.Pending {
		return job.ErrBadState
	}
	running := 0
	for _, x := range j.rungs {
		if x.state == job.Running {
			running++
		}
	}
	if running >= n.C {
		return job.ErrBusy
	}
	r.state = job.Running
	r.attempt++
	n.maxNow = now
	return nil
}

func (n *naive) finish(now int64, id, rung string, ok bool, size int64) error {
	if id == "" || rung == "" || !validNowN(now) || (ok && (size < 0 || size > 1_000_000_000_000)) {
		return ErrInvalidParam
	}
	if now < n.maxNow {
		return ErrClockSkew
	}
	j, found := n.jobs[id]
	if !found {
		return ErrJobNotFound
	}
	r := j.find(rung)
	if r == nil {
		return job.ErrRungNotFound
	}
	if r.state != job.Running {
		return job.ErrBadState
	}
	if ok {
		r.state = job.Done
		r.size = size
	} else if r.attempt <= n.R {
		r.state = job.Pending
	} else {
		r.state = job.Failed
		if r.required {
			j.failed = true
		}
	}
	// 每次从最低档重扫计算 L。
	var L []manifest.Entry
	for _, x := range j.rungs {
		switch x.state {
		case job.Done:
			L = append(L, manifest.Entry{Name: x.name, Height: x.height, Bitrate: x.bitrate, Size: x.size, Required: x.required})
		case job.Failed:
		default:
			goto done
		}
	}
done:
	if !j.failed {
		allReq := true
		inL := map[string]bool{}
		for _, e := range L {
			inL[e.Name] = true
		}
		for _, x := range j.rungs {
			if x.required && !inL[x.name] {
				allReq = false
			}
		}
		var last []manifest.Entry
		if len(j.versions) > 0 {
			last = j.versions[len(j.versions)-1]
		}
		if allReq && !entriesEq(L, last) {
			j.versions = append(j.versions, append([]manifest.Entry(nil), L...))
		}
	}
	n.maxNow = now
	return nil
}

func entriesEq(a, b []manifest.Entry) bool {
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

func (n *naive) get(id string) (int, []manifest.Entry, error) {
	if id == "" {
		return 0, nil, ErrInvalidParam
	}
	j, ok := n.jobs[id]
	if !ok {
		return 0, nil, ErrJobNotFound
	}
	if j.failed {
		return 0, nil, ErrJobFailed
	}
	if len(j.versions) == 0 {
		return 0, nil, manifest.ErrNotReady
	}
	return len(j.versions), j.versions[len(j.versions)-1], nil
}

func (n *naive) getVersion(id string, v int) ([]manifest.Entry, error) {
	if id == "" {
		return nil, ErrInvalidParam
	}
	j, ok := n.jobs[id]
	if !ok {
		return nil, ErrJobNotFound
	}
	if j.failed {
		return nil, ErrJobFailed
	}
	if v < 1 || v > len(j.versions) {
		return nil, manifest.ErrVersionNotFound
	}
	return j.versions[v-1], nil
}

// 随机操作序列生成与对照驱动。

type opKind int

const (
	opSubmit opKind = iota
	opStart
	opFinish
	opGet
	opGetVersion
)

type op struct {
	kind     opKind
	now      int64
	id, rung string
	h, b     int
	ok       bool
	size     int64
	v        int
}

func (o op) String() string {
	switch o.kind {
	case opSubmit:
		return fmt.Sprintf("Submit(now=%d id=%q h=%d b=%d)", o.now, o.id, o.h, o.b)
	case opStart:
		return fmt.Sprintf("Start(now=%d id=%q rung=%q)", o.now, o.id, o.rung)
	case opFinish:
		return fmt.Sprintf("Finish(now=%d id=%q rung=%q ok=%v size=%d)", o.now, o.id, o.rung, o.ok, o.size)
	case opGet:
		return fmt.Sprintf("Get(id=%q)", o.id)
	default:
		return fmt.Sprintf("GetVersion(id=%q v=%d)", o.id, o.v)
	}
}

type scenario struct {
	tmpl []ladder.Rung
	R, C int
	ops  []op
}

func genScenario(r *rand.Rand) scenario {
	// 模板：1..6 档，高度/码率取自常用档位的子集以制造恰等与钳制相等。
	heightPool := []int{240, 360, 480, 720, 1080, 1440, 2160, 4320}
	bitPool := []int{400, 600, 800, 1200, 2500, 3000, 5000, 8000, 16000}
	n := 1 + r.Intn(6)
	hs := append([]int(nil), heightPool...)
	bs := append([]int(nil), bitPool...)
	r.Shuffle(len(hs), func(i, j int) { hs[i], hs[j] = hs[j], hs[i] })
	r.Shuffle(len(bs), func(i, j int) { bs[i], bs[j] = bs[j], bs[i] })
	hs = hs[:n]
	bs = bs[:n]
	sortInts(hs)
	sortInts(bs)
	tmpl := make([]ladder.Rung, n)
	hasReq := false
	for i := range tmpl {
		req := r.Intn(2) == 0
		if i == n-1 && !hasReq {
			req = true
		}
		hasReq = hasReq || req
		tmpl[i] = ladder.Rung{
			Name:     fmt.Sprintf("r%d", i),
			Height:   hs[i],
			Bitrate:  bs[i],
			Required: req,
		}
	}
	sc := scenario{tmpl: tmpl, R: r.Intn(6), C: 1 + r.Intn(4)}

	// 操作序列：now 多数递增，偶发回退或越界。
	ids := []string{"j0", "j1", "j2"}
	rungNames := make([]string, n)
	for i, t := range tmpl {
		rungNames[i] = t.Name
	}
	var clock int64
	submitted := 0
	nOps := 40 + r.Intn(40)
	// 生成过程中维护一份朴素模拟，用于构造"有效推进"的操作。
	nv := newNaive(sc.tmpl, sc.R, sc.C)
	for k := 0; k < nOps; k++ {
		var o op
		clock += int64(r.Intn(6))
		o.now = clock
		if x := r.Intn(100); x < 8 {
			o.now = clock - int64(1+r.Intn(20)) // 时钟回退
		} else if x < 11 {
			o.now = 1_000_000_000_001 + int64(r.Intn(1000)) // now 越界
		}
		pickID := func() string {
			if r.Intn(100) < 6 {
				return "ghost"
			}
			return ids[r.Intn(len(ids))]
		}
		pickRung := func() string {
			if r.Intn(100) < 8 {
				return "nope"
			}
			return rungNames[r.Intn(len(rungNames))]
		}
		productive := r.Intn(100) < 55
		var o2 op
		useO2 := false
		if productive && submitted > 0 {
			o2, useO2 = productiveOp(r, nv, ids[:submitted], o.now)
		}
		switch x := r.Intn(100); {
		case useO2:
			o = o2
		case (x < 22 || submitted == 0) && submitted < len(ids):
			o.kind = opSubmit
			o.id = ids[submitted%len(ids)]
			submitted++
			// 源参数：常取模板恰等值以命中边界，偶发越界。
			if r.Intn(100) < 60 {
				o.h = hs[r.Intn(len(hs))]
			} else {
				o.h = 1 + r.Intn(4320)
			}
			if r.Intn(100) < 60 {
				o.b = bs[r.Intn(len(bs))]
			} else {
				o.b = 1 + r.Intn(20000)
			}
			if r.Intn(100) < 4 {
				o.h = 0 // 参数非法
			}
		case x < 50:
			o.kind = opStart
			o.id = pickID()
			o.rung = pickRung()
		case x < 80:
			o.kind = opFinish
			o.id = pickID()
			o.rung = pickRung()
			o.ok = r.Intn(100) < 75
			o.size = int64(r.Intn(1_000_000))
			if r.Intn(100) < 3 {
				o.size = 1_000_000_000_001 // size 越界
			}
		case x < 88:
			o.kind = opGet
			o.id = pickID()
		default:
			o.kind = opGetVersion
			o.id = pickID()
			o.v = r.Intn(6)
		}
		sc.ops = append(sc.ops, o)
		// 生成器内部的朴素模拟同步推进（仅用于产生后续有效操作）。
		switch o.kind {
		case opSubmit:
			nv.submit(o.now, o.id, o.h, o.b)
		case opStart:
			nv.start(o.now, o.id, o.rung)
		case opFinish:
			nv.finish(o.now, o.id, o.rung, o.ok, o.size)
		}
	}
	return sc
}

// productiveOp 依据朴素模拟的当前状态构造一个大概率被接受的操作。
func productiveOp(r *rand.Rand, nv *naive, ids []string, now int64) (op, bool) {
	var runningTargets []struct{ id, rung string }
	var startTargets []struct{ id, rung string }
	for _, id := range ids {
		j, ok := nv.jobs[id]
		if !ok || j.terminated() {
			continue
		}
		running := 0
		for _, x := range j.rungs {
			if x.state == job.Running {
				running++
				runningTargets = append(runningTargets, struct{ id, rung string }{id, x.name})
			}
		}
		if running < nv.C {
			for _, x := range j.rungs {
				if x.state == job.Pending {
					startTargets = append(startTargets, struct{ id, rung string }{id, x.name})
				}
			}
		}
	}
	if len(runningTargets) > 0 && (len(startTargets) == 0 || r.Intn(2) == 0) {
		tg := runningTargets[r.Intn(len(runningTargets))]
		return op{
			kind: opFinish, now: now, id: tg.id, rung: tg.rung,
			ok:   r.Intn(100) < 85,
			size: int64(r.Intn(1_000_000)),
		}, true
	}
	if len(startTargets) > 0 {
		tg := startTargets[r.Intn(len(startTargets))]
		return op{kind: opStart, now: now, id: tg.id, rung: tg.rung}, true
	}
	return op{}, false
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func runOps(t *testing.T, svc *Service, nv *naive, ops []op) {
	t.Helper()
	for k, o := range ops {
		var errSvc, errNv error
		switch o.kind {
		case opSubmit:
			errSvc = svc.Submit(o.now, o.id, o.h, o.b)
			errNv = nv.submit(o.now, o.id, o.h, o.b)
		case opStart:
			errSvc = svc.Start(o.now, o.id, o.rung)
			errNv = nv.start(o.now, o.id, o.rung)
		case opFinish:
			errSvc = svc.Finish(o.now, o.id, o.rung, o.ok, o.size)
			errNv = nv.finish(o.now, o.id, o.rung, o.ok, o.size)
		case opGet:
			numS, listS, e1 := svc.Get(o.id)
			numN, listN, e2 := nv.get(o.id)
			if !sameErr(e1, e2) || numS != numN || !entriesEq(listS, listN) {
				t.Fatalf("op#%d %s\n  实现: num=%d list=%v err=%v\n  朴素: num=%d list=%v err=%v",
					k, o, numS, listS, e1, numN, listN, e2)
			}
			continue
		case opGetVersion:
			listS, e1 := svc.GetVersion(o.id, o.v)
			listN, e2 := nv.getVersion(o.id, o.v)
			if !sameErr(e1, e2) || !entriesEq(listS, listN) {
				t.Fatalf("op#%d %s\n  实现: list=%v err=%v\n  朴素: list=%v err=%v",
					k, o, listS, e1, listN, e2)
			}
			continue
		}
		if !sameErr(errSvc, errNv) {
			t.Fatalf("op#%d %s\n  实现 err=%v\n  朴素 err=%v", k, o, errSvc, errNv)
		}
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a == b
}

// checkInvariants 校验版本真超集、Running<=C、发版作业不失败，并返回各作业版本数。
func checkInvariants(t *testing.T, svc *Service, nv *naive, c int) map[string]int {
	t.Helper()
	versions := map[string]int{}
	for id, nj := range nv.jobs {
		// 逐版枚举并与朴素模拟对照。
		var prev []manifest.Entry
		for v := 1; ; v++ {
			list, err := svc.GetVersion(id, v)
			if err != nil {
				if nj.failed {
					if err != ErrJobFailed {
						t.Fatalf("%s 失败作业 GetVersion(%d) err=%v", id, v, err)
					}
					versions[id] = 0
					break
				}
				if err != manifest.ErrVersionNotFound {
					t.Fatalf("%s GetVersion(%d) err=%v", id, v, err)
				}
				versions[id] = v - 1
				break
			}
			if !entriesEq(list, nj.versions[v-1]) {
				t.Fatalf("%s 版本 %d\n  实现: %v\n  朴素: %v", id, v, list, nj.versions[v-1])
			}
			if !strictSupersetOf(prev, list) {
				t.Fatalf("%s 版本 %d 不是前一版真超集", id, v)
			}
			prev = list
		}
		if len(nj.versions) != versions[id] {
			t.Fatalf("%s 版本数 实现=%d 朴素=%d", id, versions[id], len(nj.versions))
		}
		rec := svc.jobs[id]
		if rec.job.Running() > c {
			t.Fatalf("%s Running=%d > C=%d", id, rec.job.Running(), c)
		}
		if versions[id] > 0 && rec.job.Failed() {
			t.Fatalf("%s 发版后作业失败", id)
		}
		// Get 与最新版一致。
		num, list, err := svc.Get(id)
		nNum, nList, nErr := nv.get(id)
		if !sameErr(err, nErr) || num != nNum || !entriesEq(list, nList) {
			t.Fatalf("%s Get 不一致: 实现(%d,%v,%v) 朴素(%d,%v,%v)", id, num, list, err, nNum, nList, nErr)
		}
	}
	return versions
}

func strictSupersetOf(prev, cur []manifest.Entry) bool {
	if len(cur) <= len(prev) {
		return false
	}
	set := make(map[manifest.Entry]bool, len(cur))
	for _, e := range cur {
		set[e] = true
	}
	for _, e := range prev {
		if !set[e] {
			return false
		}
	}
	return true
}

// TestRandomAgainstNaive 1500 组随机操作序列与朴素模拟逐操作对照，
// 并对每组做重放一致性校验。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 1500
	for seed := int64(0); seed < sequences; seed++ {
		r := rand.New(rand.NewSource(seed))
		sc := genScenario(r)
		tmpl, err := ladder.NewTemplate(sc.tmpl, sc.R, sc.C)
		if err != nil {
			t.Fatalf("seed=%d 模板非法: %v", seed, err)
		}

		svc := NewService(tmpl)
		nv := newNaive(sc.tmpl, sc.R, sc.C)
		if seed < 3 {
			for k, o := range sc.ops {
				t.Logf("seed=%d 输入 op#%d %s", seed, k, o)
			}
		}
		runOps(t, svc, nv, sc.ops)
		versions := checkInvariants(t, svc, nv, sc.C)
		if seed < 3 {
			t.Logf("seed=%d 输出 版本=%v", seed, versions)
		}

		// 重放：相同操作序列作用于全新服务，各版清单完全一致。
		svc2 := NewService(tmpl)
		nv2 := newNaive(sc.tmpl, sc.R, sc.C)
		runOps(t, svc2, nv2, sc.ops)
		for id := range nv.jobs {
			for v := 1; v <= versions[id]; v++ {
				l1, e1 := svc.GetVersion(id, v)
				l2, e2 := svc2.GetVersion(id, v)
				if e1 != nil || e2 != nil || !reflect.DeepEqual(l1, l2) {
					t.Fatalf("seed=%d 重放不一致 %s 版本%d: %v vs %v", seed, id, v, l1, l2)
				}
			}
		}
		t.Logf("seed=%d 模板=%d档 R=%d C=%d 操作=%d 版本=%v | 判定: 逐操作错误与Get输出同朴素模拟一致, 版本真超集, Running<=C, 重放一致",
			seed, len(sc.tmpl), sc.R, sc.C, len(sc.ops), versions)
	}
}
