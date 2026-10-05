package ontology

import (
	"sync"

	"ontology/job"
	"ontology/ladder"
	"ontology/manifest"
)

const (
	maxNow  = 1_000_000_000_000
	maxSize = 1_000_000_000_000
)

// Service 点播转码作业服务。所有操作可并发调用，内部以单锁串行化，
// 结果等价于某个串行顺序。
type Service struct {
	mu      sync.Mutex
	tmpl    ladder.Template
	maxNow  int64
	jobs    map[string]*record
	touched int // Get/Finish 等操作触碰的作业记录数，用于证明与作业总数无关
}

type record struct {
	job      *job.Job
	manifest *manifest.Manifest
	index    map[string]int // 档名 -> 阶梯下标
	finishes int            // 被接受的 Finish 次数
}

// NewService 以校验通过的模板创建服务。
func NewService(tmpl ladder.Template) *Service {
	return &Service{tmpl: tmpl, jobs: make(map[string]*record)}
}

func validNow(now int64) bool { return 0 <= now && now <= maxNow }

// Submit 推导阶梯并创建作业。
// 拒绝次序：参数非法 > 时钟回退 > 作业已存在 > 源不足。
func (s *Service) Submit(now int64, id string, srcHeight, srcBitrate int) error {
	if id == "" || !validNow(now) ||
		srcHeight < 1 || srcHeight > ladder.MaxHeight ||
		srcBitrate < 1 || srcBitrate > ladder.MaxBitrate {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, ok := s.jobs[id]; ok {
		return ErrJobExists
	}
	lad, err := s.tmpl.Derive(srcHeight, srcBitrate)
	if err != nil {
		return err
	}
	rungs := lad.Rungs()
	jrungs := make([]job.Rung, len(rungs))
	entries := make([]manifest.Entry, len(rungs))
	idx := make(map[string]int, len(rungs))
	for i, r := range rungs {
		jrungs[i] = job.Rung{Name: r.Name, Required: r.Required}
		entries[i] = manifest.Entry{
			Name: r.Name, Height: r.Height, Bitrate: r.Bitrate, Required: r.Required,
		}
		idx[r.Name] = i
	}
	s.jobs[id] = &record{
		job:      job.New(jrungs, s.tmpl.RetryLimit(), s.tmpl.Concurrency()),
		manifest: manifest.New(entries),
		index:    idx,
	}
	s.accept(now)
	return nil
}

// Start 启动某档任务。
// 拒绝次序：参数非法 > 时钟回退 > 作业不存在 > 作业已终止 > 档不在阶梯 > 状态不符 > 繁忙。
func (s *Service) Start(now int64, id, rung string) error {
	if id == "" || rung == "" || !validNow(now) {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	rec, ok := s.jobs[id]
	if !ok {
		return ErrJobNotFound
	}
	if err := rec.job.Start(rung); err != nil {
		return err
	}
	s.accept(now)
	return nil
}

// Finish 结束某档任务。ok 为真时校验并记录 size（0..1e12）。
// 拒绝次序：参数非法 > 时钟回退 > 作业不存在 > 档不在阶梯 > 状态不符。
// 接受后重算可列集合并按条件发版；作业已失败则不再发版。
func (s *Service) Finish(now int64, id, rung string, ok bool, size int64) error {
	if id == "" || rung == "" || !validNow(now) || (ok && (size < 0 || size > maxSize)) {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	rec, found := s.jobs[id]
	if !found {
		return ErrJobNotFound
	}
	s.touched++
	terminal, err := rec.job.Finish(rung, ok, size)
	if err != nil {
		return err
	}
	rec.finishes++
	if terminal {
		rec.manifest.OnTerminal(rec.index[rung], ok, size)
		if !rec.job.Failed() {
			rec.manifest.MaybePublish()
		}
	}
	s.accept(now)
	return nil
}

// Get 返回最新版本号与清单（按高度升序）。
// 拒绝次序：参数非法 > 作业不存在 > 作业失败 > 未就绪。
func (s *Service) Get(id string) (int, []manifest.Entry, error) {
	if id == "" {
		return 0, nil, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.jobs[id]
	if !ok {
		return 0, nil, ErrJobNotFound
	}
	s.touched++
	if rec.job.Failed() {
		return 0, nil, ErrJobFailed
	}
	v, err := rec.manifest.Latest()
	if err != nil {
		return 0, nil, err
	}
	return v.Number, v.Rungs, nil
}

// GetVersion 取历史版本，v 越界报版本不存在。
// 拒绝次序：参数非法 > 作业不存在 > 作业失败 > 版本不存在。
func (s *Service) GetVersion(id string, v int) ([]manifest.Entry, error) {
	if id == "" {
		return nil, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.jobs[id]
	if !ok {
		return nil, ErrJobNotFound
	}
	s.touched++
	if rec.job.Failed() {
		return nil, ErrJobFailed
	}
	ver, err := rec.manifest.Get(v)
	if err != nil {
		return nil, err
	}
	return ver.Rungs, nil
}

func (s *Service) checkClock(now int64) error {
	if now < s.maxNow {
		return ErrClockSkew
	}
	return nil
}

func (s *Service) accept(now int64) {
	s.maxNow = now
}
