// Package manifest 负责码率阶梯推导、作业调度与清单分版发布。
package manifest

import (
	"errors"
	"sync"

	"ontology/job"
	"ontology/ladder"
)

var (
	// ErrInvalidArgument 参数非法（模板、id、now、size 等）。
	ErrInvalidArgument = errors.New("manifest: invalid argument")
	// ErrClockRewind now 小于已接受操作的最大 now。
	ErrClockRewind = errors.New("manifest: clock rewind")
	// ErrJobExists Submit 时作业 id 已存在。
	ErrJobExists = errors.New("manifest: job already exists")
	// ErrJobNotFound 作业不存在。
	ErrJobNotFound = errors.New("manifest: job not found")
	// ErrSourceInsufficient 源高度导致 required 档在第一步被丢弃。
	ErrSourceInsufficient = ladder.ErrSourceInsufficient
	// ErrTerminated 作业已终止（完成或失败），不能再 Start。
	ErrTerminated = job.ErrTerminated
	// ErrNotReady 从未发布过任何版本。
	ErrNotReady = errors.New("manifest: no manifest version yet")
	// ErrJobFailed 作业已失败。
	ErrJobFailed = errors.New("manifest: job failed")
	// ErrVersionNotFound 请求的历史版本不存在。
	ErrVersionNotFound = errors.New("manifest: version not found")
)

// Publisher 是作业与清单版本的总管。
type Publisher struct {
	mu      sync.Mutex
	rungs   []ladder.Rung
	maxTry  int
	conc    int
	now     int64
	jobs    map[string]*entry
	advance int64
	touched int64
}

// Rung 是模板档定义。
type Rung struct {
	Name     string
	Height   int
	Bitrate  int
	Required bool
}

// entry 是单个作业的运行记录（骨架阶段占位）。
type entry struct {
	job      *job.Job
	rungs    []ladder.Rung
	frontier int // 已确定不在未来 L 中的档数（只进不退）
	versions [][]Rung
	failed   bool
}

// New 创建发布器。
func New(rungs []Rung, retry, concurrency int, now int64) (*Publisher, error) {
	lrungs := make([]ladder.Rung, len(rungs))
	for i, r := range rungs {
		lrungs[i] = ladder.Rung(r)
	}
	tmpl, err := ladder.NewTemplate(lrungs)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	if retry < 0 || retry > 5 || concurrency < 1 || concurrency > 16 || now < 0 || now > 1e12 {
		return nil, ErrInvalidArgument
	}
	return &Publisher{
		rungs:  tmpl.Rungs,
		maxTry: retry + 1,
		conc:   concurrency,
		now:    now,
		jobs:   make(map[string]*entry),
	}, nil
}

func validNow(now int64) bool { return now >= 0 && now <= 1e12 }

// Submit 推导作业阶梯并登记作业。
// 拒绝次序：参数非法 > 时钟回退 > 作业已存在 > 源不足。
func (p *Publisher) Submit(now int64, id string, srcHeight, srcBitrate int) error {
	if id == "" || !validNow(now) || srcHeight < 1 || srcHeight > 4320 ||
		srcBitrate < 1 || srcBitrate > 1e8 {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.now {
		return ErrClockRewind
	}
	if _, ok := p.jobs[id]; ok {
		return ErrJobExists
	}
	tmpl := &ladder.Template{Rungs: p.rungs}
	derived, err := tmpl.Derive(srcHeight, srcBitrate)
	if err != nil {
		return err
	}
	p.now = now
	p.jobs[id] = &entry{
		job:   job.New(id, derived, p.maxTry),
		rungs: derived,
	}
	return nil
}

// Start 启动一档任务。
// 拒绝次序：参数非法 > 时钟回退 > 作业不存在 > 作业已终止 > 档不在阶梯 > 状态不符 > 繁忙。
func (p *Publisher) Start(now int64, id, rung string) error {
	if id == "" || rung == "" || !validNow(now) {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.now {
		return ErrClockRewind
	}
	e, ok := p.jobs[id]
	if !ok {
		return ErrJobNotFound
	}
	p.touched++
	if e.failed || e.job.Done() {
		return ErrTerminated
	}
	if err := e.job.Start(rung, p.conc); err != nil {
		return mapJobErr(err)
	}
	p.now = now
	return nil
}

// Finish 提交一档任务结果，随后重算前沿并按需发版。
// 拒绝次序：参数非法 > 时钟回退 > 作业不存在 > 档不在阶梯 > 状态不符。
func (p *Publisher) Finish(now int64, id, rung string, ok bool, size int64) error {
	if id == "" || rung == "" || !validNow(now) || size < 0 || size > 1e12 {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.now {
		return ErrClockRewind
	}
	e, exists := p.jobs[id]
	if !exists {
		return ErrJobNotFound
	}
	p.touched++
	if _, found := indexOf(e.rungs, rung); !found {
		return mapJobErr(job.ErrRungNotFound)
	}
	if err := e.job.Finish(rung, ok, size); err != nil {
		return mapJobErr(err)
	}
	p.now = now

	snap := e.job.Snapshot()
	requiredFailed := false
	for _, t := range snap {
		if t.Rung.Required && t.State == job.Failed {
			requiredFailed = true
		}
	}
	if requiredFailed {
		e.failed = true
		return nil
	}

	// 前沿只进不退：每个被检查的档计入 advance。
	for e.frontier < len(e.rungs) {
		p.advance++
		st := snap[e.frontier].State
		if st == job.Pending || st == job.Running {
			break
		}
		e.frontier++
	}

	l := make([]Rung, 0, e.frontier)
	for i := 0; i < e.frontier; i++ {
		if snap[i].State == job.Done {
			l = append(l, Rung(snap[i].Rung))
		}
	}
	if !containsAllRequired(l, e.rungs) {
		return nil
	}
	if sameManifest(l, e.lastVersion()) {
		return nil
	}
	version := make([]Rung, len(l))
	copy(version, l)
	e.versions = append(e.versions, version)
	return nil
}

// Get 返回最新版本号与清单。
func (p *Publisher) Get(id string) (int, []Rung, error) {
	if id == "" {
		return 0, nil, ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.jobs[id]
	if !ok {
		return 0, nil, ErrNotReady
	}
	p.touched++
	if e.failed {
		return 0, nil, ErrJobFailed
	}
	if len(e.versions) == 0 {
		return 0, nil, ErrNotReady
	}
	v := e.versions[len(e.versions)-1]
	out := make([]Rung, len(v))
	copy(out, v)
	return len(e.versions), out, nil
}

// GetVersion 取历史版本（v 从 1 起）。
func (p *Publisher) GetVersion(id string, v int) ([]Rung, error) {
	if id == "" || v < 1 {
		return nil, ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.jobs[id]
	if !ok || e.failed || v > len(e.versions) {
		return nil, ErrVersionNotFound
	}
	out := make([]Rung, len(e.versions[v-1]))
	copy(out, e.versions[v-1])
	return out, nil
}

// AdvanceCount 返回全部作业前沿检查累计次数。
func (p *Publisher) AdvanceCount() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.advance
}

// TouchedCount 返回 Get/Finish 触碰作业记录的累计计数。
func (p *Publisher) TouchedCount() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.touched
}

func (e *entry) lastVersion() []Rung {
	if len(e.versions) == 0 {
		return nil
	}
	return e.versions[len(e.versions)-1]
}

func indexOf(rungs []ladder.Rung, name string) (int, bool) {
	for i := range rungs {
		if rungs[i].Name == name {
			return i, true
		}
	}
	return 0, false
}

func containsAllRequired(l []Rung, all []ladder.Rung) bool {
	have := make(map[string]bool, len(l))
	for _, r := range l {
		have[r.Name] = true
	}
	for _, r := range all {
		if r.Required && !have[r.Name] {
			return false
		}
	}
	return true
}

func sameManifest(a, b []Rung) bool {
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

func mapJobErr(err error) error {
	switch {
	case errors.Is(err, job.ErrRungNotFound):
		return ErrRungNotFound
	case errors.Is(err, job.ErrNotPending):
		return ErrNotPending
	case errors.Is(err, job.ErrNotRunning):
		return ErrNotRunning
	case errors.Is(err, job.ErrBusy):
		return ErrBusy
	default:
		return err
	}
}

// ErrRungNotFound 档不在阶梯。
var ErrRungNotFound = job.ErrRungNotFound

// ErrNotPending 档不是 Pending。
var ErrNotPending = job.ErrNotPending

// ErrNotRunning 档不是 Running。
var ErrNotRunning = job.ErrNotRunning

// ErrBusy 并行上限已满。
var ErrBusy = job.ErrBusy
