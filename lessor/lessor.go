package lessor

import (
	"fmt"
	"sync"
)

const (
	maxNow = int64(1_000_000_000_000_000) // now 上界 10^15
)

// Config 构造参数，越界时 New 整体拒绝。
type Config struct {
	MinTTL int64 // 最小 TTL，[1, 10^6]
	MaxTTL int64 // 最大 TTL，[MinTTL, 10^9]
	E      int64 // 切换宽限，[0, 10^9]
	R      int64 // 每次 Tick 的撤销上限，[1, 10^6]
	Kmax   int64 // 每租约挂靠键上限，[1, 10^6]
}

func (c Config) validate() error {
	if c.MinTTL < 1 || c.MinTTL > 1_000_000 {
		return fmt.Errorf("%w: MinTTL %d out of [1, 10^6]", ErrInvalidParam, c.MinTTL)
	}
	if c.MaxTTL < c.MinTTL || c.MaxTTL > 1_000_000_000 {
		return fmt.Errorf("%w: MaxTTL %d out of [MinTTL, 10^9]", ErrInvalidParam, c.MaxTTL)
	}
	if c.E < 0 || c.E > 1_000_000_000 {
		return fmt.Errorf("%w: E %d out of [0, 10^9]", ErrInvalidParam, c.E)
	}
	if c.R < 1 || c.R > 1_000_000 {
		return fmt.Errorf("%w: R %d out of [1, 10^6]", ErrInvalidParam, c.R)
	}
	if c.Kmax < 1 || c.Kmax > 1_000_000 {
		return fmt.Errorf("%w: Kmax %d out of [1, 10^6]", ErrInvalidParam, c.Kmax)
	}
	return nil
}

// Lessor etcd 式租约管理器。所有方法可并发调用，效果等价于某串行顺序。
type Lessor struct {
	mu      sync.Mutex
	cfg     Config
	primary bool // 节点初始为从
	T       int64
	leases  map[int64]*Lease
	byKey   map[string]int64 // 键 -> 租约编号，保证每键至多挂一个租约
	h       leaseHeap

	// tickInspected 非导出计数器：本次 Tick 检视的堆顶数，恒 <= 撤销数+1。
	tickInspected int
}

// New 构造 Lessor；配置越界时整体拒绝。
func New(cfg Config) (*Lessor, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Lessor{
		cfg:    cfg,
		leases: make(map[int64]*Lease),
		byKey:  make(map[string]int64),
	}, nil
}

func (l *Lessor) checkID(id int64) error {
	if id < 1 {
		return fmt.Errorf("%w: id %d < 1", ErrInvalidParam, id)
	}
	return nil
}

func (l *Lessor) checkTime(now int64) error {
	if now < 0 || now > maxNow {
		return fmt.Errorf("%w: now %d out of [0, 10^15]", ErrInvalidTime, now)
	}
	if now < l.T {
		return fmt.Errorf("%w: now %d < T %d", ErrInvalidTime, now, l.T)
	}
	return nil
}

func (l *Lessor) checkPrimary() error {
	if !l.primary {
		return fmt.Errorf("%w: follower cannot serve this op", ErrRole)
	}
	return nil
}
