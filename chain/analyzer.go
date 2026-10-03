package chain

// analyzer.go: 并发安全的任务与链注册表、参数校验、Analyze / Tune。

import (
	"errors"
	"sync"
)

type Task struct {
	ID         string
	Period     int
	Phase      int
	WriteDelay int
}

type Analysis struct {
	MaxReaction int
	MinReaction int
	MaxAge      int
}

type TuneResult struct {
	BeforeMaxReaction int
	AfterMaxReaction  int
	Changed           bool
	BeforePhases      []int
	AfterPhases       []int
}

// Analyzer 保存全部任务与链。所有公开方法在同一把 RWMutex 下执行，
// 因而并发调用的结果等价于某个串行顺序（线性化）。
type Analyzer struct {
	mu     sync.RWMutex
	tasks  map[string]Task
	order  []string // 任务加入顺序，保证重放确定性
	chains map[string][]string
	corder []string // 链加入顺序
}

func NewAnalyzer() *Analyzer {
	return &Analyzer{tasks: map[string]Task{}, chains: map[string][]string{}}
}

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrNotFound        = errors.New("not found")
	ErrDuplicate       = errors.New("duplicate")
	ErrCapacityFull    = errors.New("capacity full")
	ErrTooLarge        = errors.New("scale too large")
	ErrInUse           = errors.New("task referenced by a chain")
	ErrShared          = errors.New("chain shares task with another chain")
)

const (
	MaxTasks       = 16
	MaxHyperperiod = 5000
	MaxTuneProduct = 1000
	MinChainLen    = 2
	MaxChainLen    = 6
	MinIDBytes     = 1
	MaxIDBytes     = 32
)

func validID(id string) bool {
	return len(id) >= MinIDBytes && len(id) <= MaxIDBytes
}

func validPeriod(p int) bool     { return p >= 1 && p <= 1000 }
func validPhase(phi, p int) bool { return phi >= 0 && phi < p }
func validDelay(w, p int) bool   { return w >= 1 && w <= p }

// chainParams 收集一条链上各任务的 T/φ/w，顺序与链定义一致。
func (a *Analyzer) chainParams(ids []string) (periods, phases, writeDelays []int) {
	for _, id := range ids {
		t := a.tasks[id]
		periods = append(periods, t.Period)
		phases = append(phases, t.Phase)
		writeDelays = append(writeDelays, t.WriteDelay)
	}
	return
}

func (a *Analyzer) AddTask(t Task) error {
	if !validID(t.ID) || !validPeriod(t.Period) ||
		!validPhase(t.Phase, t.Period) || !validDelay(t.WriteDelay, t.Period) {
		return ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.tasks[t.ID]; exists {
		return ErrDuplicate
	}
	if len(a.order) >= MaxTasks {
		return ErrCapacityFull
	}
	a.tasks[t.ID] = t
	a.order = append(a.order, t.ID)
	return nil
}

func (a *Analyzer) RemoveTask(id string) error {
	if !validID(id) {
		return ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.tasks[id]; !exists {
		return ErrNotFound
	}
	for _, name := range a.corder {
		for _, tid := range a.chains[name] {
			if tid == id {
				return ErrInUse
			}
		}
	}
	delete(a.tasks, id)
	for i, v := range a.order {
		if v == id {
			a.order = append(a.order[:i], a.order[i+1:]...)
			break
		}
	}
	return nil
}

func (a *Analyzer) AddChain(name string, taskIDs []string) error {
	if name == "" || len(taskIDs) < MinChainLen || len(taskIDs) > MaxChainLen {
		return ErrInvalidArgument
	}
	for _, id := range taskIDs {
		if !validID(id) {
			return ErrInvalidArgument
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.chains[name]; exists {
		return ErrDuplicate
	}
	seen := map[string]bool{}
	for _, id := range taskIDs {
		if _, exists := a.tasks[id]; !exists {
			return ErrNotFound
		}
		if seen[id] {
			return ErrDuplicate
		}
		seen[id] = true
	}
	periods, _, _ := a.chainParams(taskIDs)
	if hyperperiod(periods) > MaxHyperperiod {
		return ErrTooLarge
	}
	a.chains[name] = append([]string(nil), taskIDs...)
	a.corder = append(a.corder, name)
	return nil
}

func (a *Analyzer) RemoveChain(name string) error {
	if name == "" {
		return ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.chains[name]; !exists {
		return ErrNotFound
	}
	delete(a.chains, name)
	for i, v := range a.corder {
		if v == name {
			a.corder = append(a.corder[:i], a.corder[i+1:]...)
			break
		}
	}
	return nil
}

func (a *Analyzer) SetPhase(id string, phase int) error {
	if !validID(id) {
		return ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	t, exists := a.tasks[id]
	if !exists {
		return ErrNotFound
	}
	if !validPhase(phase, t.Period) {
		return ErrInvalidArgument
	}
	t.Phase = phase
	a.tasks[id] = t
	return nil
}

func (a *Analyzer) SetDelay(id string, delay int) error {
	if !validID(id) {
		return ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	t, exists := a.tasks[id]
	if !exists {
		return ErrNotFound
	}
	if !validDelay(delay, t.Period) {
		return ErrInvalidArgument
	}
	t.WriteDelay = delay
	a.tasks[id] = t
	return nil
}

func (a *Analyzer) Analyze(chainName string) (Analysis, error) {
	if chainName == "" {
		return Analysis{}, ErrInvalidArgument
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	ids, exists := a.chains[chainName]
	if !exists {
		return Analysis{}, ErrNotFound
	}
	periods, phases, writeDelays := a.chainParams(ids)
	if hyperperiod(periods) > MaxHyperperiod {
		return Analysis{}, ErrTooLarge
	}
	return analyzeChain(periods, phases, writeDelays), nil
}

// Tune 只调整 τ2..τn 的相位；τ1 固定。按 φ2..φn 的字典序穷举，
// 严格更小才提交，并列时保留首个（即字典序最小的）最优相位向量。
func (a *Analyzer) Tune(chainName string) (TuneResult, error) {
	if chainName == "" {
		return TuneResult{}, ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	ids, exists := a.chains[chainName]
	if !exists {
		return TuneResult{}, ErrNotFound
	}
	periods, phases, writeDelays := a.chainParams(ids)
	if hyperperiod(periods) > MaxHyperperiod {
		return TuneResult{}, ErrTooLarge
	}
	product := 1
	for k := 1; k < len(ids); k++ {
		product *= periods[k]
	}
	if product > MaxTuneProduct {
		return TuneResult{}, ErrTooLarge
	}
	for _, other := range a.corder {
		if other == chainName {
			continue
		}
		set := map[string]bool{}
		for _, sid := range a.chains[other] {
			set[sid] = true
		}
		for _, id := range ids {
			if set[id] {
				return TuneResult{}, ErrShared
			}
		}
	}

	before := analyzeChain(periods, phases, writeDelays)
	beforePhases := append([]int(nil), phases...)
	result := TuneResult{
		BeforeMaxReaction: before.MaxReaction,
		AfterMaxReaction:  before.MaxReaction,
		Changed:           false,
		BeforePhases:      beforePhases,
		AfterPhases:       append([]int(nil), phases...),
	}

	n := len(ids)
	candidate := append([]int(nil), phases...)
	bestMax := before.MaxReaction
	var bestPhases []int

	var enumerate func(idx int)
	enumerate = func(idx int) {
		if idx == n {
			cur := analyzeChain(periods, candidate, writeDelays).MaxReaction
			if cur < bestMax {
				bestMax = cur
				bestPhases = append([]int(nil), candidate...)
			}
			return
		}
		for phi := 0; phi < periods[idx]; phi++ {
			candidate[idx] = phi
			enumerate(idx + 1)
		}
		candidate[idx] = phases[idx]
	}
	enumerate(1)

	if bestPhases != nil {
		for k := 1; k < n; k++ {
			t := a.tasks[ids[k]]
			t.Phase = bestPhases[k]
			a.tasks[ids[k]] = t
		}
		result.AfterMaxReaction = bestMax
		result.AfterPhases = append([]int(nil), bestPhases...)
		result.Changed = true
	}
	return result, nil
}

// Snapshot 返回按加入顺序排列的任务副本与链副本，便于复现与断言。
func (a *Analyzer) Snapshot() (tasks []Task, chains map[string][]string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, id := range a.order {
		tasks = append(tasks, a.tasks[id])
	}
	chains = map[string][]string{}
	for _, name := range a.corder {
		chains[name] = append([]string(nil), a.chains[name]...)
	}
	return
}
