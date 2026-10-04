// Package quarantine 提供带不稳定用例隔离的测试分片规划器。
// 所有方法可并发调用，结果等价于某个串行顺序。
package quarantine

import (
	"errors"
	"sort"
	"sync"

	"ontology/history"
	"ontology/shard"
)

// 各类被拒绝操作的错误，可用 errors.Is 区分。
var (
	ErrInvalidParam  = errors.New("quarantine: invalid parameter")
	ErrBuildExists   = errors.New("quarantine: build already exists")
	ErrBuildNotFound = errors.New("quarantine: build not found")
	ErrBuildFinished = errors.New("quarantine: build already finished")
	ErrTestNotInPlan = errors.New("quarantine: test not in plan")
	ErrState         = errors.New("quarantine: attempt state mismatch")
	ErrIncomplete    = errors.New("quarantine: build has unfinished tests")
)

// Verdict 是构建的最终判定。
type Verdict int

const (
	Passed Verdict = iota // 构建通过
	Failed                // 构建失败
)

func (v Verdict) String() string {
	if v == Failed {
		return "Failed"
	}
	return "Passed"
}

const maxMS = int64(1_000_000_000)

const maxTests = 10_000

// attempt 记录一个用例在一次构建内的尝试状态。
type attempt struct {
	tries int
	done  bool // 已有最终结果
	mark  history.Mark
	ms    int64 // 通过那一次的时长
}

// build 记录一次构建的计划与进行状态。
type build struct {
	tests    []string            // 计划内用例（Plan 时的次序）
	snapshot map[string]bool     // Plan 时刻每个用例是否被隔离
	attempts map[string]*attempt // 每个用例的尝试状态
	finished bool
}

// Planner 是分片规划器。
type Planner struct {
	mu      sync.Mutex
	r       int // 失败后的重试次数
	hist    *history.Store
	builds  map[string]*build
	touched int // 非导出计数器：一次 Report 读写的记录数
}

// New 创建规划器：W 为标记窗口长度（1..50），F 为隔离阈值（1..W），
// P 为解除所需连续干净次数（1..20），R 为失败后的重试次数（0..3），
// D 为时长样本数（1..10）。
func New(W, F, P, R, D int) (*Planner, error) {
	if W < 1 || W > 50 || F < 1 || F > W || P < 1 || P > 20 || R < 0 || R > 3 || D < 1 || D > 10 {
		return nil, ErrInvalidParam
	}
	return &Planner{
		r:      R,
		hist:   history.NewStore(W, F, P, D),
		builds: make(map[string]*build),
	}, nil
}

// Plan 为构建 build 规划 tests 的分片：返回 N 个常规分片，
// 若有被隔离用例（按 Plan 此刻快照）则追加一个按名字升序的隔离分片。
func (p *Planner) Plan(buildID string, tests []string, n int) ([][]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if buildID == "" || len(tests) < 1 || len(tests) > maxTests || n < 1 || n > 256 {
		return nil, ErrInvalidParam
	}
	seen := make(map[string]bool, len(tests))
	for _, t := range tests {
		if t == "" || seen[t] {
			return nil, ErrInvalidParam
		}
		seen[t] = true
	}
	if _, ok := p.builds[buildID]; ok {
		return nil, ErrBuildExists
	}
	snapshot := make(map[string]bool, len(tests))
	ests := make(map[string]int64, len(tests))
	var regular, quarantined []string
	var sampleEsts []int64
	for _, t := range tests {
		q := p.hist.Quarantined(t)
		snapshot[t] = q
		if q {
			quarantined = append(quarantined, t)
		} else {
			regular = append(regular, t)
		}
		if e, ok := p.hist.Est(t); ok {
			ests[t] = e
			if !q {
				sampleEsts = append(sampleEsts, e)
			}
		}
	}
	def := shard.LowerMedian(sampleEsts)
	est := func(t string) int64 {
		if e, ok := ests[t]; ok {
			return e
		}
		return def
	}
	shards := shard.Assign(regular, est, n)
	if len(quarantined) > 0 {
		sort.Strings(quarantined)
		shards = append(shards, quarantined)
	}
	p.builds[buildID] = &build{
		tests:    append([]string(nil), tests...),
		snapshot: snapshot,
		attempts: make(map[string]*attempt),
	}
	return shards, nil
}

// Report 上报一次尝试结果：ms 为 0 到 10^9。每个用例在一次构建内最多
// R+1 次尝试，一旦通过或用尽即有最终结果，其后再报为状态不符。
func (p *Planner) Report(buildID, test string, pass bool, ms int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.touched = 0
	if ms < 0 || ms > maxMS {
		return ErrInvalidParam
	}
	b, ok := p.builds[buildID]
	p.touched++
	if !ok {
		return ErrBuildNotFound
	}
	if b.finished {
		return ErrBuildFinished
	}
	if _, inPlan := b.snapshot[test]; !inPlan {
		return ErrTestNotInPlan
	}
	a := b.attempts[test]
	if a == nil {
		a = &attempt{}
		b.attempts[test] = a
	}
	p.touched++
	if a.done {
		return ErrState
	}
	a.tries++
	switch {
	case pass && a.tries == 1:
		a.done, a.mark, a.ms = true, history.Clean, ms
	case pass:
		a.done, a.mark, a.ms = true, history.Flaky, ms
	case a.tries > p.r:
		a.done, a.mark = true, history.Broken
	}
	return nil
}

// Finish 结束构建：计划内每个用例都须已有最终结果。判定只用 Plan 时的
// 快照：快照中未隔离的用例有 Broken 则 Failed，否则 Passed。随后按名字
// 升序以 Finish 此刻的实时隔离状态更新历史。
func (p *Planner) Finish(buildID string) (Verdict, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b, ok := p.builds[buildID]
	if !ok {
		return Passed, ErrBuildNotFound
	}
	if b.finished {
		return Passed, ErrBuildFinished
	}
	for _, t := range b.tests {
		a := b.attempts[t]
		if a == nil || !a.done {
			return Passed, ErrIncomplete
		}
	}
	verdict := Passed
	for _, t := range b.tests {
		if !b.snapshot[t] && b.attempts[t].mark == history.Broken {
			verdict = Failed
			break
		}
	}
	b.finished = true
	sorted := append([]string(nil), b.tests...)
	sort.Strings(sorted)
	for _, t := range sorted {
		a := b.attempts[t]
		p.hist.Update(t, a.mark, a.ms)
	}
	return verdict, nil
}
