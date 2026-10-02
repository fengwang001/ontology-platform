package compactor

import (
	"errors"
	"math/big"
	"sync"
)

// ErrParam 表示参数非法。
var ErrParam = errors.New("compactor: invalid parameter")

// ErrClock 表示时钟回退。
var ErrClock = errors.New("compactor: clock moved backwards")

// ErrBusy 表示未结束计划已达 Cmax 上限。
var ErrBusy = errors.New("compactor: too many active plans")

// ErrUnknown 表示计划不存在或已经结束。
var ErrUnknown = errors.New("compactor: unknown or finished plan")

// ErrNotNeeded 表示四条规则均不成立，当前无需压实。
var ErrNotNeeded = errors.New("compactor: no compaction needed")

// Reason 标识压实计划由哪条规则产生。
type Reason string

// 四条规则的标识常量。
const (
	ReasonSpaceAmp    Reason = "SpaceAmp"
	ReasonSizeRatio   Reason = "SizeRatio"
	ReasonCountReduce Reason = "CountReduce"
	ReasonPeriodic    Reason = "Periodic"
)

// Run 表示压实选择器中的一个运行。
type Run struct {
	ID      int64
	Size    int64
	Created int64
	Busy    bool
	Fails   int
}

// Plan 是一次 Pick 产生的压实计划。
type Plan struct {
	ID     int64
	Reason Reason
	Runs   []int64
	Total  int64
}

// Config 是选择器配置。
type Config struct {
	MinRuns  int
	MaxRuns  int
	A        int64
	Rho      int64
	MinMerge int
	MaxMerge int
	Cmax     int
	P        int64
}

// Selector 是并发安全的 Universal 风格压实选择器。
type Selector struct {
	mu sync.Mutex

	cfg Config

	runs []*Run

	nextRunID int64
	nextPlan  int64
	now       int64

	plans  map[int64]*activePlan
	active int

	probes int64
}

type activePlan struct {
	runs []int64
}

// New 按配置创建选择器，非法配置返回 ErrParam。
func New(cfg Config) (*Selector, error) {
	if cfg.MinRuns < 2 || cfg.MaxRuns < cfg.MinRuns {
		return nil, ErrParam
	}
	if cfg.A < 1 || cfg.A > 1_000_000 {
		return nil, ErrParam
	}
	if cfg.Rho < 0 || cfg.Rho > 10_000 {
		return nil, ErrParam
	}
	if cfg.MinMerge < 2 || cfg.MaxMerge < cfg.MinMerge {
		return nil, ErrParam
	}
	if cfg.Cmax < 1 {
		return nil, ErrParam
	}
	if cfg.P < 0 {
		return nil, ErrParam
	}
	return &Selector{
		cfg:       cfg,
		plans:     make(map[int64]*activePlan),
		nextRunID: 1,
		nextPlan:  1,
	}, nil
}

// AddRun 在最新端追加一个运行并返回其编号。
func (s *Selector) AddRun(now, size int64) (int64, error) {
	if size < 1 || size > 1_000_000_000_000 || now < 0 {
		return 0, ErrParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return 0, ErrClock
	}
	s.now = now
	id := s.nextRunID
	s.nextRunID++
	// 新运行放在最新端（下标 0）。
	s.runs = append([]*Run{{ID: id, Size: size, Created: now}}, s.runs...)
	return id, nil
}

// Pick 依四条规则的次序挑选一个压实计划。
func (s *Selector) Pick(now int64) (Plan, error) {
	if now < 0 {
		return Plan{}, ErrParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return Plan{}, ErrClock
	}
	if s.active >= s.cfg.Cmax {
		return Plan{}, ErrBusy
	}

	n := len(s.runs)

	// 规则一 SpaceAmp：n >= MinRuns 且没有任何忙运行，
	// E*100 >= A*S（S 为最旧运行大小，E 为其余之和，恰等成立）。
	if n >= s.cfg.MinRuns {
		anyBusy := false
		for _, r := range s.runs {
			if r.Busy {
				anyBusy = true
				break
			}
		}
		if !anyBusy {
			oldest := s.runs[n-1]
			var e int64
			for i := 0; i < n-1; i++ {
				e += s.runs[i].Size
			}
			// E*100 >= A*S 成立（恰等触发），即 A*S <= E*100。
			if leBig(s.cfg.A, oldest.Size, e, 100) {
				return s.commit(now, ReasonSpaceAmp, s.runs[:n:n])
			}
		}
	}

	// 规则二 SizeRatio：从最新端依次考察起点；忙运行与 fails>=2 的运行
	// 既不能作起点也不能被并入。延伸条件 size*100 <= acc*(100+Rho)，
	// 恰等延伸；个数受 MaxMerge 截断，达到 MinMerge 即选中。
	if n >= s.cfg.MinRuns {
		for i := 0; i < n; i++ {
			s.probes++
			start := s.runs[i]
			if start.Busy || start.Fails >= 2 {
				continue
			}
			acc := start.Size
			count := 1
			j := i + 1
			for j < n && count < s.cfg.MaxMerge {
				s.probes++
				next := s.runs[j]
				if next.Busy || next.Fails >= 2 {
					break
				}
				if !leBig(next.Size, 100, acc, 100+s.cfg.Rho) {
					break
				}
				acc += next.Size
				count++
				j++
			}
			if count >= s.cfg.MinMerge {
				return s.commit(now, ReasonSizeRatio, s.runs[i:j:j])
			}
		}
	}

	// 规则三 CountReduce：n > MaxRuns 时，
	// c = min(n-MaxRuns+1, MaxMerge)，取最新端起第一个连续 c 个不忙窗口。
	if n > s.cfg.MaxRuns {
		c := n - s.cfg.MaxRuns + 1
		if s.cfg.MaxMerge < c {
			c = s.cfg.MaxMerge
		}
		for i := 0; i+c <= n; i++ {
			ok := true
			for k := 0; k < c; k++ {
				if s.runs[i+k].Busy {
					ok = false
					break
				}
			}
			if ok {
				return s.commit(now, ReasonCountReduce, s.runs[i:i+c:i+c])
			}
		}
	}

	// 规则四 Periodic：P>0 时取位置最旧、不忙且 now-created >= P（恰等成立）者。
	if s.cfg.P > 0 {
		for i := n - 1; i >= 0; i-- {
			r := s.runs[i]
			if !r.Busy && now-r.Created >= s.cfg.P {
				return s.commit(now, ReasonPeriodic, s.runs[i:i+1:i+1])
			}
		}
	}

	return Plan{}, ErrNotNeeded
}

// commit 以给定运行段生成计划：置忙、推进时钟、登记生命周期。
func (s *Selector) commit(now int64, reason Reason, seg []*Run) (Plan, error) {
	ids := make([]int64, len(seg))
	var total int64
	planRuns := make([]int64, len(seg))
	for i, r := range seg {
		r.Busy = true
		ids[i] = r.ID
		total += r.Size
		planRuns[i] = r.ID
	}
	id := s.nextPlan
	s.nextPlan++
	s.plans[id] = &activePlan{runs: planRuns}
	s.active++
	s.now = now
	return Plan{ID: id, Reason: reason, Runs: ids, Total: total}, nil
}

// Done 用一个新运行整体替换计划所覆盖的运行段。
func (s *Selector) Done(now, planID, out int64) error {
	if now < 0 || planID < 1 || out < 1 || out > 1_000_000_000_000 {
		return ErrParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return ErrClock
	}
	plan, ok := s.plans[planID]
	if !ok {
		return ErrUnknown
	}

	// 计划按运行身份跟踪：段在计划产生时连续，之后若在最新端插入新运行，
	// 下标会整体平移，因此每次都用编号重新定位。
	pos := make(map[int64]int, len(s.runs))
	for i, r := range s.runs {
		pos[r.ID] = i
	}
	start := pos[plan.runs[0]]
	end := pos[plan.runs[len(plan.runs)-1]] + 1
	for _, id := range plan.runs {
		if idx, ok := pos[id]; !ok || idx < start || idx >= end {
			return ErrUnknown
		}
	}

	id := s.nextRunID
	s.nextRunID++
	repl := &Run{ID: id, Size: out, Created: now}

	updated := make([]*Run, 0, len(s.runs)-len(plan.runs)+1)
	updated = append(updated, s.runs[:start]...)
	updated = append(updated, repl)
	updated = append(updated, s.runs[end:]...)
	s.runs = updated

	delete(s.plans, planID)
	s.active--
	s.now = now
	return nil
}

// Abort 取消计划，解除忙标记并累加失败计数。
func (s *Selector) Abort(planID int64) error {
	if planID < 1 {
		return ErrParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[planID]
	if !ok {
		return ErrUnknown
	}
	for _, r := range s.runs {
		for _, id := range plan.runs {
			if r.ID == id {
				r.Busy = false
				r.Fails++
			}
		}
	}
	delete(s.plans, planID)
	s.active--
	return nil
}

// Runs 返回当前运行集合的副本（按新到旧排列）。
func (s *Selector) Runs() []Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Run, len(s.runs))
	for i, r := range s.runs {
		out[i] = *r
	}
	return out
}

// Probes 返回 SizeRatio 累计考察次数：每取一个起点计 1，
// 每检查一个待并入的下一个运行计 1。单次 Pick 增量不超过 n*MaxMerge。
func (s *Selector) Probes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.probes
}

// leBig 判断 a*b <= c*d，乘积经 big.Int 计算，杜绝溢出与取等歧义。
func leBig(a, b, c, d int64) bool {
	l := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	r := new(big.Int).Mul(big.NewInt(c), big.NewInt(d))
	return l.Cmp(r) <= 0
}
