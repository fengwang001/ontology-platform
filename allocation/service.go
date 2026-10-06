package allocation

import "sort"

// Service 高校教师授课任务分配与学期工作量核算服务。
// 所有方法可并发调用，内部用单把互斥锁串行化，等价于某个串行顺序；
// 每个写操作必须携带单调不减的逻辑时间 now。
type Service struct {
	impl *internalService
}

func NewService(cfg Config) *Service {
	if err := validateConfig(cfg); err != nil {
		panic(err)
	}
	return &Service{impl: &internalService{
		cfg:      cfg,
		teachers: make(map[string]*teacher),
		tasks:    make(map[string]*task),
		shares:   make(map[int64]*share),
		settled:  make(map[string]*SemesterReport),
	}}
}

func validateConfig(cfg Config) *Error {
	if cfg.ConfirmDeadline < 0 {
		return newErr(ErrInvalidArgument, "negative confirm deadline")
	}
	if cfg.NewCourseBonusPercent < 0 || cfg.LabPercent <= 0 ||
		len(cfg.Tiers) == 0 || len(cfg.Ranks) == 0 {
		return newErr(ErrInvalidArgument, "bad coefficients or empty tiers/ranks")
	}
	prev := -1
	for _, t := range cfg.Tiers {
		if t.MinSize < 0 || t.Percent <= 0 || t.MinSize <= prev {
			return newErr(ErrInvalidArgument, "bad scale tier")
		}
		prev = t.MinSize
	}
	ranks := map[string]bool{}
	for _, r := range cfg.Ranks {
		if r.Rank == "" || r.Min < 0 || r.Max < r.Min || ranks[r.Rank] {
			return newErr(ErrInvalidArgument, "bad rank limit")
		}
		ranks[r.Rank] = true
	}
	return nil
}

func (s *Service) SetTracer(t Tracer) {
	svc := s.impl
	svc.mu.Lock()
	defer svc.mu.Unlock()
	svc.tracer = t
}

// checkClock 时钟回退优先级仅次于参数非法。
func (svc *internalService) checkClock(now int64) *Error {
	if now < svc.clock {
		return newErr(ErrClockRollback, "now=%d clock=%d", now, svc.clock)
	}
	return nil
}

func uniqueSortedPeriods(in []int) ([]int, *Error) {
	seen := make(map[int]bool)
	var out []int
	for _, p := range in {
		if p <= 0 {
			return nil, newErr(ErrInvalidArgument, "bad period %d", p)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, newErr(ErrInvalidArgument, "no periods")
	}
	sort.Ints(out)
	return out, nil
}
