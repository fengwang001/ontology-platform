package scheduler

import (
	"errors"
	"fmt"

	"ontology/cascade"
	"ontology/timer"
)

// 可判定的哨兵错误。
var (
	ErrNegativeDelay    = errors.New("scheduler: negative delay")
	ErrInvalidAdvance   = errors.New("scheduler: advance ticks must be positive")
	ErrAdvanceTooLarge  = errors.New("scheduler: advance exceeds max ticks")
	ErrTooManyTimers    = errors.New("scheduler: too many timers")
	ErrDelayTooLarge    = errors.New("scheduler: delay exceeds max")
	ErrAlreadyCancelled = errors.New("scheduler: timer already cancelled")
	ErrAlreadyFired     = errors.New("scheduler: timer already fired")
	ErrUnknownTimer     = errors.New("scheduler: unknown timer")
	ErrInvalidConfig    = errors.New("scheduler: invalid config")
)

// Config 调度器配置，零值字段取默认值。
type Config struct {
	Start      int64 // 注入的起始时刻
	Levels     int   // 层数，默认 4
	SlotSize   int   // 每层槽数，默认 64
	MaxAdvance int64 // 单次 Advance 最大 tick 数，默认 1<<20
	MaxTimers  int   // 同时存在的定时器上限，默认 1<<20
	MaxDelay   int64 // 单定时器最大延迟，默认轮系总量程
}

// New 创建调度器；配置非法或 MaxDelay 超出轮系量程时返回错误。
func New(cfg Config) (*Scheduler, error) {
	if cfg.Levels == 0 {
		cfg.Levels = 4
	}
	if cfg.SlotSize == 0 {
		cfg.SlotSize = 64
	}
	if cfg.MaxAdvance == 0 {
		cfg.MaxAdvance = 1 << 20
	}
	if cfg.MaxTimers == 0 {
		cfg.MaxTimers = 1 << 20
	}
	if cfg.Levels < 1 || cfg.SlotSize < 2 || cfg.MaxAdvance < 1 || cfg.MaxTimers < 1 || cfg.MaxDelay < 0 {
		return nil, ErrInvalidConfig
	}
	layout := cascade.Layout{Levels: cfg.Levels, Size: cfg.SlotSize}
	if cfg.MaxDelay == 0 {
		cfg.MaxDelay = layout.MaxDelay()
	}
	if cfg.MaxDelay > layout.MaxDelay() {
		return nil, fmt.Errorf("%w: MaxDelay %d > range %d", ErrInvalidConfig, cfg.MaxDelay, layout.MaxDelay())
	}
	s := &Scheduler{
		now: cfg.Start, layout: layout, maxAdv: cfg.MaxAdvance,
		maxTm: cfg.MaxTimers, maxDelay: cfg.MaxDelay,
		pending: make(map[*timer.Timer]struct{}),
	}
	s.wheels = layout.Build(cfg.Start)
	return s, nil
}
