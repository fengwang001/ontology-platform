package sampler

import "errors"

// Config 是采样器的构造参数。
type Config struct {
	K           int64 // 采样间隔，[1,1e6]
	Cm          int   // 镜像并发上限，[1,1e6]
	Bm          int64 // 请求体上限，[0,1e9]
	E           int   // 连续失败阈值，[1,1e6]
	P           int64 // 暂停时长（毫秒），[1,1e9]
	AllowUnsafe bool  // 是否允许非安全方法
}

// SkipReason 是 Mirror 未分派的跳过原因。
type SkipReason int

const (
	Unsafe SkipReason = iota + 1
	BodyTooLarge
	Paused
	NotSelected
	Busy
)

var ErrInvalidConfig = errors.New("sampler: invalid config")
var (
	ErrInvalidTime = errors.New("sampler: invalid time")
	ErrClockBack   = errors.New("sampler: clock went backwards")
)

// Sampler 是纯判定内核，非并发安全（由调用方加锁）。
type Sampler struct {
	cfg         Config
	c           int64 // 合格计数
	s           int   // 连续失败数
	pausedUntil int64
	maxNow      int64
}

// New 校验构造参数并返回采样器。
func New(cfg Config) (*Sampler, error) {
	if cfg.K < 1 || cfg.K > 1_000_000 ||
		cfg.Cm < 1 || cfg.Cm > 1_000_000 ||
		cfg.Bm < 0 || cfg.Bm > 1_000_000_000 ||
		cfg.E < 1 || cfg.E > 1_000_000 ||
		cfg.P < 1 || cfg.P > 1_000_000_000 {
		return nil, ErrInvalidConfig
	}
	return &Sampler{cfg: cfg}, nil
}

// CheckClock 校验时间合法且不回退；通过时推进单调时钟。
func (s *Sampler) CheckClock(now int64) error {
	if now < 0 || now > 1_000_000_000_000_000 {
		return ErrInvalidTime
	}
	if now < s.maxNow {
		return ErrClockBack
	}
	s.maxNow = now
	return nil
}

// Filter 按序判定一次 Mirror 是否可分派。调用前须已通过 CheckClock。
// inFlight 为当前镜像在途数；返回 selected=true 表示调用方应分派。
func (s *Sampler) Filter(method string, bodyLen int64, now int64, inFlight int) (reason SkipReason, selected bool) {
	safe := method == "GET" || method == "HEAD"
	if !safe && !s.cfg.AllowUnsafe {
		return Unsafe, false
	}
	if bodyLen > s.cfg.Bm {
		return BodyTooLarge, false
	}
	if now < s.pausedUntil {
		return Paused, false
	}
	s.c++
	if s.c%s.cfg.K != 0 {
		return NotSelected, false
	}
	if inFlight >= s.cfg.Cm {
		return Busy, false
	}
	return 0, true
}

// Fail 登记一次镜像错误并按规则触发暂停。
func (s *Sampler) Fail(now int64) {
	if now < s.pausedUntil {
		return
	}
	s.s++
	if s.s >= s.cfg.E {
		s.pausedUntil = now + s.cfg.P
		s.s = 0
	}
}

// Success 登记一次成功镜像（暂停期外清零连续失败）。
func (s *Sampler) Success(now int64) {
	if now >= s.pausedUntil {
		s.s = 0
	}
}

// C 返回合格计数。
func (s *Sampler) C() int64 { return s.c }

// PausedUntil 返回当前暂停截止时刻。
func (s *Sampler) PausedUntil() int64 { return s.pausedUntil }

// Cm 返回并发上限。
func (s *Sampler) Cm() int { return s.cfg.Cm }
