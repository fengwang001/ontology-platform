package delay

import (
	"sort"
)

// newEngine 校验航班表与配置，构建链条并做首次整表推算。
func newEngine(flights []Flight, cfg Config) (*Engine, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	e := &Engine{
		cfg:     cfg,
		byID:    make(map[string]*flight),
		chainsA: make(map[string][]*flight),
		chainsC: make(map[string][]*flight),
		inj:     make(map[string]map[string]Injection),
		clock:   -1,
	}
	seen := make(map[string]bool)
	for i, f := range flights {
		if f.ID == "" || seen[f.ID] {
			return nil, ErrInvalid
		}
		seen[f.ID] = true
		if f.Scheduled < 0 || f.Duration <= 0 || f.Origin == "" || f.Dest == "" ||
			f.AircraftID == "" || f.CrewID == "" {
			return nil, ErrInvalid
		}
		if cf, ok := cfg.Curfews[f.Origin]; ok && !validInterval(cf) {
			return nil, ErrInvalid
		}
		if cf, ok := cfg.Curfews[f.Dest]; ok && !validInterval(cf) {
			return nil, ErrInvalid
		}
		pf := &flight{Flight: f, ord: i, piA: -1, piC: -1}
		e.flights = append(e.flights, pf)
		e.byID[f.ID] = pf
	}
	if err := buildChains(e, e.chainsA, true); err != nil {
		return nil, err
	}
	if err := buildChains(e, e.chainsC, false); err != nil {
		return nil, err
	}
	e.recompute(nil, -1)
	return e, nil
}

func validateConfig(cfg Config) error {
	if cfg.MinTurnaround < 0 || cfg.MinConnection < 0 || cfg.DutyLimit < 0 {
		return ErrInvalid
	}
	for _, cf := range cfg.Curfews {
		if !validInterval(cf) {
			return ErrInvalid
		}
	}
	return nil
}

func validInterval(cf Interval) bool { return cf.Start >= 0 && cf.End >= cf.Start }

// buildChains 按计划起飞时刻（同分按输入序号）排序并校验机场连续性。
func buildChains(e *Engine, chains map[string][]*flight, aircraft bool) error {
	for _, f := range e.flights {
		key := f.CrewID
		if aircraft {
			key = f.AircraftID
		}
		chains[key] = append(chains[key], f)
	}
	for key, list := range chains {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].Scheduled != list[j].Scheduled {
				return list[i].Scheduled < list[j].Scheduled
			}
			return list[i].ord < list[j].ord
		})
		for i := range list {
			pos := i
			if aircraft {
				list[i].piA = pos
			} else {
				list[i].piC = pos
			}
		}
		for i := 1; i < len(list); i++ {
			if list[i-1].Dest != list[i].Origin {
				return ErrInvalid
			}
		}
		_ = key
	}
	return nil
}

// mutate 在统一的拒绝次序下处理一次注入或撤回，成功后增量重算。
func (e *Engine) mutate(now int, flightID, ref string, inj *Injection, withdraw bool) error {
	if now < 0 {
		return ErrInvalid
	}
	if !withdraw && inj.Delay < 0 {
		return ErrInvalid
	}
	if now < e.clock {
		return ErrClockRewind
	}
	f, ok := e.byID[flightID]
	if !ok {
		return ErrNoFlight
	}
	if c := e.conc[f.ID]; c.status != StatusCanceled && c.dep <= now {
		return ErrDeparted
	}
	refs := e.inj[flightID]
	if withdraw {
		if _, exists := refs[ref]; !exists {
			return ErrNoInjection
		}
	}
	if refs == nil {
		refs = make(map[string]Injection)
		e.inj[flightID] = refs
	}
	if withdraw {
		delete(refs, ref)
		if len(refs) == 0 {
			delete(e.inj, flightID)
		}
	} else {
		refs[ref] = *inj
	}
	e.clock = now
	e.recompute(map[string]bool{f.ID: true}, now)
	return nil
}

// InjectDelay 以 ref 为可撤回引用注入延误（delay>=0，多次同 ref 覆盖）。
func (e *Engine) InjectDelay(now int, flightID, ref string, delay int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mutate(now, flightID, ref, &Injection{Delay: delay}, false)
}

// InjectCancel 以 ref 为可撤回引用注入取消。
func (e *Engine) InjectCancel(now int, flightID, ref string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mutate(now, flightID, ref, &Injection{Cancel: true}, false)
}

// Withdraw 撤回此前注入的引用。
func (e *Engine) Withdraw(now int, flightID, ref string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mutate(now, flightID, ref, nil, true)
}

// Get 查询单个航班结论，复杂度 O(1)。
func (e *Engine) Get(flightID string) (Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	f, ok := e.byID[flightID]
	if !ok {
		return Result{}, ErrNoFlight
	}
	c := e.conc[f.ID]
	return Result{Status: c.status, ActualDep: c.dep, ActualArr: c.arr, Reason: c.reason}, nil
}

// LastSwept 返回最近一次重算实际处理的航班数量（性能可验证计数）。
func (e *Engine) LastSwept() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastSwept
}

// touchClock 仅推进时钟（供时钟推进但无注入的场景使用）。
func (e *Engine) touchClock(now int) error {
	if now < e.clock {
		return ErrClockRewind
	}
	e.clock = now
	return nil
}
