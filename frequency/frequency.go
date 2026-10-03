// Package frequency implements per-user ad exposure frequency control.
//
// Each request checks creative replay interval, global window capacity, and
// campaign daily capacity in that order. Peek is read-only. AdmitBatch applies
// its sequential requests as one transaction, rolling all of them back if any
// request is rejected.
package frequency

import (
	"fmt"
	"sync"
)

type Reason string

const (
	ReasonOK               Reason = ""
	ReasonInvalidArgument  Reason = "invalid_argument"
	ReasonClockRollback    Reason = "clock_rollback"
	ReasonCreativeInterval Reason = "creative_interval"
	ReasonGlobalWindow     Reason = "global_window"
	ReasonCampaignDaily    Reason = "campaign_daily"
)

type Config struct {
	GlobalWindow       int64
	GlobalLimit        int64
	TighteningStep     int64
	CampaignDailyLimit int64
	CreativeBaseGap    int64
}

type Request struct {
	Campaign string
	Creative string
	Now      int64
}

type Evidence struct {
	LastTime         int64
	LastCreative     string
	CreativeRun      int
	WindowCount      int64
	RequiredGap      int64
	Elapsed          int64
	EffectiveDaily   int64
	CampaignDayCount int64
	Day              int64
}

type Decision struct {
	Allowed  bool
	Reason   Reason
	Evidence Evidence
}

type BatchResult struct {
	Allowed  bool
	Index    int
	Reason   Reason
	Evidence Evidence
}

type Controller struct {
	config Config

	mu    sync.RWMutex
	users map[string]*userState
}

type exposure struct {
	time     int64
	campaign string
	creative string
}

type userState struct {
	mu sync.Mutex

	// records 按时间非递减保存当前仍可能用于判定的已放行曝光。
	records []exposure

	// creativeRun 不随全局窗口过期重置；lastTime 是最近一次已放行时刻。
	lastTime     int64
	lastCreative string
	creativeRun  int

	day   int64
	daily map[string]int64

	popped int64
}

func NewController(globalWindow, globalLimit, tighteningStep, campaignDailyLimit, creativeBaseGap int64) (*Controller, error) {
	cfg := Config{
		GlobalWindow:       globalWindow,
		GlobalLimit:        globalLimit,
		TighteningStep:     tighteningStep,
		CampaignDailyLimit: campaignDailyLimit,
		CreativeBaseGap:    creativeBaseGap,
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Controller{config: cfg, users: make(map[string]*userState)}, nil
}

func (c Config) validate() error {
	if c.GlobalWindow < 1 || c.GlobalWindow > 1_000_000_000 ||
		c.GlobalLimit < 1 || c.GlobalLimit > 1_000_000 ||
		c.TighteningStep < 1 || c.TighteningStep > 1_000_000 ||
		c.CampaignDailyLimit < 1 || c.CampaignDailyLimit > 1_000_000 ||
		c.CreativeBaseGap < 1 || c.CreativeBaseGap > 1_000_000_000 {
		return fmt.Errorf("%w: frequency config out of range", ErrInvalidArgument)
	}
	return nil
}

func (c *Controller) Admit(user, campaign, creative string, now int64) Decision {
	if reason := validateRequest(user, campaign, creative, now); reason != ReasonOK {
		return Decision{Reason: reason}
	}

	state := c.user(user)
	state.mu.Lock()
	defer state.mu.Unlock()

	simulation := state.snapshot()
	decision := simulateOne(c.config, simulation, Request{Campaign: campaign, Creative: creative, Now: now})
	if !decision.Allowed {
		return decision
	}
	simulation.commit(state)
	return decision
}

func (c *Controller) Peek(user, campaign, creative string, now int64) Decision {
	if reason := validateRequest(user, campaign, creative, now); reason != ReasonOK {
		return Decision{Reason: reason}
	}

	state := c.user(user)
	state.mu.Lock()
	defer state.mu.Unlock()

	return simulateOne(c.config, state.snapshot(), Request{Campaign: campaign, Creative: creative, Now: now})
}

func (c *Controller) AdmitBatch(user string, requests []Request) BatchResult {
	if user == "" || len(requests) < 1 || len(requests) > 1000 {
		return BatchResult{Index: -1, Reason: ReasonInvalidArgument}
	}
	for i, req := range requests {
		if reason := validateRequest(user, req.Campaign, req.Creative, req.Now); reason != ReasonOK {
			return BatchResult{Index: i, Reason: reason}
		}
	}

	state := c.user(user)
	state.mu.Lock()
	defer state.mu.Unlock()

	simulation := state.snapshot()
	for i, req := range requests {
		decision := simulateOne(c.config, simulation, req)
		if !decision.Allowed {
			return BatchResult{Index: i, Reason: decision.Reason, Evidence: decision.Evidence}
		}
	}
	simulation.commit(state)
	return BatchResult{Allowed: true, Index: -1}
}

func validateRequest(user, campaign, creative string, now int64) Reason {
	if user == "" || campaign == "" || creative == "" || now < 0 || now > 1_000_000_000_000_000 {
		return ReasonInvalidArgument
	}
	return ReasonOK
}

func (c *Controller) user(user string) *userState {
	c.mu.RLock()
	state, ok := c.users[user]
	c.mu.RUnlock()
	if ok {
		return state
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if state, ok := c.users[user]; ok {
		return state
	}
	state = &userState{day: -1, daily: make(map[string]int64)}
	c.users[user] = state
	return state
}

type simulatedState struct {
	records      []exposure
	lastTime     int64
	lastCreative string
	creativeRun  int
	day          int64
	daily        map[string]int64
	popped       int64
}

func (s *userState) snapshot() *simulatedState {
	records := make([]exposure, len(s.records))
	copy(records, s.records)

	daily := make(map[string]int64, len(s.daily))
	for campaign, count := range s.daily {
		daily[campaign] = count
	}

	return &simulatedState{
		records:      records,
		lastTime:     s.lastTime,
		lastCreative: s.lastCreative,
		creativeRun:  s.creativeRun,
		day:          s.day,
		daily:        daily,
		popped:       s.popped,
	}
}

func simulateOne(cfg Config, s *simulatedState, req Request) Decision {
	evidence := Evidence{
		LastTime:     s.lastTime,
		LastCreative: s.lastCreative,
		CreativeRun:  s.creativeRun,
		Day:          req.Now / 86400,
	}

	if s.creativeRun > 0 && req.Now < s.lastTime {
		return Decision{Reason: ReasonClockRollback, Evidence: evidence}
	}

	if s.creativeRun > 0 && s.lastCreative == req.Creative {
		multiplier := int64(min(s.creativeRun, 3))
		evidence.RequiredGap = cfg.CreativeBaseGap * multiplier
		evidence.Elapsed = req.Now - s.lastTime
		if evidence.Elapsed < evidence.RequiredGap {
			return Decision{Reason: ReasonCreativeInterval, Evidence: evidence}
		}
	}

	windowCount := s.windowCount(req.Now, cfg.GlobalWindow)
	evidence.WindowCount = windowCount
	if windowCount >= cfg.GlobalLimit {
		return Decision{Reason: ReasonGlobalWindow, Evidence: evidence}
	}

	s.rollDay(req.Now / 86400)
	dayCount := s.daily[req.Campaign]
	effective := cfg.CampaignDailyLimit - windowCount/cfg.TighteningStep
	if effective < 1 {
		effective = 1
	}
	evidence.CampaignDayCount = dayCount
	evidence.EffectiveDaily = effective
	if dayCount >= effective {
		return Decision{Reason: ReasonCampaignDaily, Evidence: evidence}
	}

	decision := Decision{Allowed: true, Evidence: evidence}
	s.records = append(s.records, exposure{time: req.Now, campaign: req.Campaign, creative: req.Creative})
	if s.creativeRun == 0 || s.lastCreative != req.Creative {
		s.creativeRun = 1
	} else {
		s.creativeRun++
	}
	s.lastTime = req.Now
	s.lastCreative = req.Creative
	s.daily[req.Campaign]++
	return decision
}

func (s *simulatedState) windowCount(now, globalWindow int64) int64 {
	for len(s.records) > 0 && s.records[0].time+globalWindow <= now {
		s.records = s.records[1:]
		s.popped++
	}
	return int64(len(s.records))
}

func (s *simulatedState) rollDay(day int64) {
	if s.day != day {
		s.day = day
		clear(s.daily)
	}
}

func (s *simulatedState) commit(target *userState) {
	committed := make([]exposure, len(s.records))
	copy(committed, s.records)
	target.records = committed
	target.lastTime = s.lastTime
	target.lastCreative = s.lastCreative
	target.creativeRun = s.creativeRun
	target.day = s.day
	target.daily = s.daily
	target.popped = s.popped
}
