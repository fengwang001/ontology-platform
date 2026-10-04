// Package probe 维护设备温度读数，按阶梯读数与缺口规则提供累计加权超温分钟查询。
package probe

import (
	"errors"
	"sort"
	"sync"
)

// 各类拒绝原因，可用 errors.Is 区分。
var (
	ErrInvalidParam  = errors.New("invalid parameter")
	ErrClockBack     = errors.New("clock rollback")
	ErrNoReleasePerm = errors.New("missing release permission")
	ErrNotFound      = errors.New("not found")
	ErrConflict      = errors.New("conflict")
	ErrState         = errors.New("state mismatch")
	ErrSpoiled       = errors.New("spoiled")
	ErrNoQAPerm      = errors.New("missing QA permission")
)

const (
	// MaxNow 是 now 的最大合法值。
	MaxNow = int64(1_000_000_000)
	// MinTemp 与 MaxTemp 是温度（0.1℃ 整数）的合法范围。
	MinTemp = int64(-2730)
	MaxTemp = int64(10000)
)

// ValidNow 报告 now 是否在 [0, MaxNow] 内。
func ValidNow(now int64) bool { return now >= 0 && now <= MaxNow }

// Clock 是全局单调时钟，三个包共用。所有公开操作在时钟锁内串行，
// 从而并发调用等价于某个串行顺序。
type Clock struct {
	mu  sync.Mutex
	max int64
}

// Lock 与 Unlock 串行化所有共享该时钟的操作。
func (c *Clock) Lock()   { c.mu.Lock() }
func (c *Clock) Unlock() { c.mu.Unlock() }

// Check 校验 now 不小于已接受操作的最大 now。调用方须持有锁。
func (c *Clock) Check(now int64) error {
	if now < c.max {
		return ErrClockBack
	}
	return nil
}

// Advance 在操作被接受后推进时钟。调用方须持有锁。
func (c *Clock) Advance(now int64) {
	if now > c.max {
		c.max = now
	}
}

// Config 是档位判定参数，温度单位为 0.1℃ 的整数。
type Config struct {
	Hi    int64 // 温度上限
	Delta int64 // 轻度带宽，>= 1
	W     int64 // 重度权重，2..10
	G     int64 // 读数缺口阈值（分钟），1..1e6
}

// Validate 校验构造参数。
func (c Config) Validate() error {
	if c.Hi < MinTemp || c.Hi > MaxTemp || c.Delta < 1 || c.Hi+c.Delta > MaxTemp ||
		c.W < 2 || c.W > 10 || c.G < 1 || c.G > 1_000_000 {
		return ErrInvalidParam
	}
	return nil
}

// weight 返回读数 temp 的分钟权重：正常 0，轻度 1，重度 W。
func (c Config) weight(temp int64) int64 {
	switch {
	case temp <= c.Hi:
		return 0
	case temp <= c.Hi+c.Delta:
		return 1
	default:
		return c.W
	}
}

type dev struct {
	times []int64
	temps []int64
	pref  []int64 // pref[i] = [times[0], times[i]) 的累计加权分钟
}

// Store 是设备读数存储。除 Reading 外的方法均须在时钟锁内调用。
type Store struct {
	clock   *Clock
	cfg     Config
	devices map[string]*dev
	touched int64 // 非导出计数器：查询访问的读数记录数，用于证明二分定位
}

// NewStore 构造读数存储。
func NewStore(clock *Clock, cfg Config) (*Store, error) {
	if clock == nil {
		return nil, ErrInvalidParam
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Store{clock: clock, cfg: cfg, devices: make(map[string]*dev)}, nil
}

// Config 返回档位参数。
func (s *Store) Config() Config { return s.cfg }

// Reading 给设备追加一条读数；同一设备读数时刻须严格递增。
func (s *Store) Reading(device string, temp, now int64) error {
	if device == "" || temp < MinTemp || temp > MaxTemp || !ValidNow(now) {
		return ErrInvalidParam
	}
	s.clock.Lock()
	defer s.clock.Unlock()
	if err := s.clock.Check(now); err != nil {
		return err
	}
	d := s.devices[device]
	if d == nil {
		d = &dev{}
		s.devices[device] = d
	}
	if n := len(d.times); n > 0 {
		s.touched++ // 只读取最后一条记录，常数触碰
		last := n - 1
		if now == d.times[last] {
			return ErrState
		}
		// now > d.times[last] 由时钟单调保证。
		cov := now - d.times[last]
		gap := int64(0)
		if cov > s.cfg.G {
			gap = cov - s.cfg.G
			cov = s.cfg.G
		}
		p := d.pref[last] + s.cfg.weight(d.temps[last])*cov + s.cfg.W*gap
		d.times = append(d.times, now)
		d.temps = append(d.temps, temp)
		d.pref = append(d.pref, p)
	} else {
		d.times = append(d.times, now)
		d.temps = append(d.temps, temp)
		d.pref = append(d.pref, 0)
	}
	s.clock.Advance(now)
	return nil
}

// HasReadings 报告设备是否已有至少一条读数。调用方须持有时钟锁。
func (s *Store) HasReadings(device string) bool {
	return s.devices[device] != nil
}

// Weight 返回设备自首条读数时刻到 x（不含）的累计加权分钟。调用方须持有时钟锁。
func (s *Store) Weight(device string, x int64) int64 {
	d := s.devices[device]
	if d == nil {
		return 0
	}
	i := sort.Search(len(d.times), func(j int) bool {
		s.touched++
		return d.times[j] > x
	}) - 1
	if i < 0 {
		return 0
	}
	s.touched++
	base := d.pref[i]
	dt := x - d.times[i]
	if dt <= s.cfg.G {
		return base + s.cfg.weight(d.temps[i])*dt
	}
	return base + s.cfg.weight(d.temps[i])*s.cfg.G + s.cfg.W*(dt-s.cfg.G)
}

// CrossAfter 返回使 Weight(device, x) > threshold 的最小 x，且 x <= limit；
// 前提是 Weight 在评估起点处不超过 threshold（Weight 单调不减）。
// 不存在时 ok 为 false。调用方须持有时钟锁。
func (s *Store) CrossAfter(device string, limit, threshold int64) (x int64, ok bool) {
	d := s.devices[device]
	if d == nil {
		return 0, false
	}
	// 定位最后一条 pref[i] <= threshold 的读数，跨越必落在其覆盖段或缺口段。
	i := sort.Search(len(d.pref), func(j int) bool {
		s.touched++
		return d.pref[j] > threshold
	}) - 1
	if i < 0 {
		return 0, false
	}
	s.touched++
	base := d.pref[i]
	r := s.cfg.weight(d.temps[i])
	cov := s.cfg.G
	if i+1 < len(d.times) {
		s.touched++
		if dt := d.times[i+1] - d.times[i]; dt < cov {
			cov = dt
		}
	}
	rem := threshold - base
	if r > 0 && rem < r*cov {
		// 覆盖段内：最小 k 使 r*k > rem。
		x = d.times[i] + rem/r + 1
	} else {
		// 缺口段内：最小 k 使 W*k > rem - r*cov。
		x = d.times[i] + cov + (rem-r*cov)/s.cfg.W + 1
	}
	if x > limit {
		return 0, false
	}
	return x, true
}
