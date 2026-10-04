// Package lessor 实现一个 etcd 式租约管理器：
// 授予带 TTL 的租约、续约、挂靠键，并在到期时按限速撤销。
// 主从切换时借助检查点剩余寿命精确恢复到期时刻。
package lessor

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 参数与时间上限。
const (
	maxNow    = int64(1_000_000_000_000_000) // now 合法上界 10^15
	minMinTTL = int64(1)
	maxMinTTL = int64(1_000_000)
	maxMaxTTL = int64(1_000_000_000)
	maxE      = int64(1_000_000_000)
	minRate   = 1
	maxRate   = 1_000_000
	minKeyMax = 1
	maxKeyMax = 1_000_000
)

// 哨兵错误，按「参数非法 -> 时间非法 -> 角色错误 -> 操作自身」的顺序判定。
var (
	ErrInvalidID    = errors.New("lessor: 参数非法: id 小于 1")
	ErrEmptyKey     = errors.New("lessor: 参数非法: key 为空")
	ErrInvalidTTL   = errors.New("lessor: 参数非法: ttl 越界")
	ErrInvalidTime  = errors.New("lessor: 时间非法: now 越界或小于时钟水位")
	ErrNotPrimary   = errors.New("lessor: 角色错误: 当前为从节点")
	ErrAlreadyPrim  = errors.New("lessor: 角色错误: 已是主节点")
	ErrAlreadyFoll  = errors.New("lessor: 角色错误: 已是从节点")
	ErrLeaseExists  = errors.New("lessor: 租约已存在")
	ErrLeaseMissing = errors.New("lessor: 租约不存在")
	ErrLeaseExpired = errors.New("lessor: 租约已过期")
	ErrLeaseFull    = errors.New("lessor: 租约挂靠键已满")
)

// Config 是 Lessor 的构造参数，任一越界则整体拒绝。
type Config struct {
	MinTTL int64 // 最小 TTL，[1, 10^6]
	MaxTTL int64 // 最大 TTL，[MinTTL, 10^9]
	E      int64 // 主从切换宽限，[0, 10^9]
	R      int   // 每次 Tick 的撤销上限，[1, 10^6]
	Kmax   int   // 每租约挂靠键上限，[1, 10^6]
}

func (c Config) validate() error {
	if c.MinTTL < minMinTTL || c.MinTTL > maxMinTTL {
		return fmt.Errorf("lessor: 配置非法: MinTTL=%d 不在 [%d,%d]", c.MinTTL, minMinTTL, maxMinTTL)
	}
	if c.MaxTTL < c.MinTTL || c.MaxTTL > maxMaxTTL {
		return fmt.Errorf("lessor: 配置非法: MaxTTL=%d 不在 [%d,%d]", c.MaxTTL, c.MinTTL, maxMaxTTL)
	}
	if c.E < 0 || c.E > maxE {
		return fmt.Errorf("lessor: 配置非法: E=%d 不在 [0,%d]", c.E, maxE)
	}
	if c.R < minRate || c.R > maxRate {
		return fmt.Errorf("lessor: 配置非法: R=%d 不在 [%d,%d]", c.R, minRate, maxRate)
	}
	if c.Kmax < minKeyMax || c.Kmax > maxKeyMax {
		return fmt.Errorf("lessor: 配置非法: Kmax=%d 不在 [%d,%d]", c.Kmax, minKeyMax, maxKeyMax)
	}
	return nil
}

// lease 是一个租约。
type lease struct {
	id   int64
	g    int64 // 有效 TTL = max(授予 ttl, MinTTL)
	sv   int64 // 检查点剩余寿命
	x    int64 // 到期时刻（仅主有意义）
	keys map[string]struct{}
}

// Revoked 是 Tick 撤销一个租约的结果。
type Revoked struct {
	ID   int64
	Keys []string // 升序
}

// Lessor 是租约管理器，所有方法可并发调用，效果等价于某个串行顺序。
type Lessor struct {
	mu      sync.Mutex
	cfg     Config
	primary bool
	now     int64 // 时钟水位 T
	leases  map[int64]*lease
	keyTo   map[string]int64 // key -> 挂靠的租约 id
	exp     leaseHeap

	tickInspected int // 非导出计数器：最近一次 Tick 检视的堆顶数
}

// New 构造一个 Lessor，初始为从节点；配置越界则整体拒绝。
func New(cfg Config) (*Lessor, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Lessor{
		cfg:    cfg,
		leases: make(map[int64]*lease),
		keyTo:  make(map[string]int64),
		exp:    leaseHeap{pos: make(map[int64]int)},
	}, nil
}

// checkTime 校验 now 合法且不早于水位 T。
func (l *Lessor) checkTime(now int64) error {
	if now < 0 || now > maxNow || now < l.now {
		return ErrInvalidTime
	}
	return nil
}

// Grant 授予租约，返回有效 TTL g = max(ttl, MinTTL)。
func (l *Lessor) Grant(id, ttl, now int64) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id < 1 {
		return 0, ErrInvalidID
	}
	if ttl < 1 || ttl > l.cfg.MaxTTL {
		return 0, ErrInvalidTTL
	}
	if err := l.checkTime(now); err != nil {
		return 0, err
	}
	if !l.primary {
		return 0, ErrNotPrimary
	}
	if _, ok := l.leases[id]; ok {
		return 0, ErrLeaseExists
	}
	g := max(ttl, l.cfg.MinTTL)
	ls := &lease{id: id, g: g, x: now + g, keys: make(map[string]struct{})}
	l.leases[id] = ls
	l.exp.push(ls)
	l.now = now
	return g, nil
}

// Renew 续约，成功返回有效 TTL g 并清零检查点剩余寿命。
func (l *Lessor) Renew(id, now int64) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id < 1 {
		return 0, ErrInvalidID
	}
	if err := l.checkTime(now); err != nil {
		return 0, err
	}
	if !l.primary {
		return 0, ErrNotPrimary
	}
	ls, ok := l.leases[id]
	if !ok {
		return 0, ErrLeaseMissing
	}
	if ls.x <= now {
		return 0, ErrLeaseExpired
	}
	ls.x = now + ls.g
	ls.sv = 0
	l.exp.fix(ls)
	l.now = now
	return ls.g, nil
}

// Attach 把 key 挂靠到租约 id。
func (l *Lessor) Attach(key string, id, now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id < 1 {
		return ErrInvalidID
	}
	if key == "" {
		return ErrEmptyKey
	}
	if err := l.checkTime(now); err != nil {
		return err
	}
	if !l.primary {
		return ErrNotPrimary
	}
	ls, ok := l.leases[id]
	if !ok {
		return ErrLeaseMissing
	}
	if ls.x <= now {
		return ErrLeaseExpired
	}
	if _, ok := ls.keys[key]; ok {
		l.now = now
		return nil // 已在该租约上，无操作成功
	}
	if len(ls.keys) >= l.cfg.Kmax {
		return ErrLeaseFull // 先判满，不摘除
	}
	if old, ok := l.keyTo[key]; ok {
		delete(l.leases[old].keys, key) // 从原租约摘除
	}
	ls.keys[key] = struct{}{}
	l.keyTo[key] = id
	l.now = now
	return nil
}

// Tick 撤销全部到期租约中按 (x, id) 升序的前 R 个，其余留作积压。
func (l *Lessor) Tick(now int64) ([]Revoked, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkTime(now); err != nil {
		return nil, err
	}
	if !l.primary {
		return nil, ErrNotPrimary
	}
	l.tickInspected = 0
	var out []Revoked
	for len(out) < l.cfg.R {
		top := l.exp.top()
		if top == nil {
			break
		}
		l.tickInspected++
		if top.x > now {
			break
		}
		l.exp.remove(top)
		out = append(out, l.destroy(top))
	}
	l.now = now
	return out, nil
}

// Revoke 立即撤销租约，无视到期，不计入 R，返回其键（升序）。
func (l *Lessor) Revoke(id, now int64) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id < 1 {
		return nil, ErrInvalidID
	}
	if err := l.checkTime(now); err != nil {
		return nil, err
	}
	if !l.primary {
		return nil, ErrNotPrimary
	}
	ls, ok := l.leases[id]
	if !ok {
		return nil, ErrLeaseMissing
	}
	l.exp.remove(ls)
	rev := l.destroy(ls)
	l.now = now
	return rev.Keys, nil
}

// destroy 删除租约并解除其全部键的挂靠，调用前须已从堆中移除。
func (l *Lessor) destroy(ls *lease) Revoked {
	keys := make([]string, 0, len(ls.keys))
	for k := range ls.keys {
		keys = append(keys, k)
		delete(l.keyTo, k)
	}
	sort.Strings(keys)
	delete(l.leases, ls.id)
	return Revoked{ID: ls.id, Keys: keys}
}

// Checkpoint 对未到期租约记录剩余寿命 sv = x - now。
func (l *Lessor) Checkpoint(now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkTime(now); err != nil {
		return err
	}
	if !l.primary {
		return ErrNotPrimary
	}
	for _, ls := range l.leases {
		if ls.x > now {
			ls.sv = ls.x - now
		}
	}
	l.now = now
	return nil
}

// Demote 使主变从，不改任何租约。
func (l *Lessor) Demote(now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkTime(now); err != nil {
		return err
	}
	if !l.primary {
		return ErrAlreadyFoll
	}
	l.primary = false
	l.now = now
	return nil
}

// Promote 使从变主，并对每个租约（含积压）重算到期时刻
// x = now + E + (sv > 0 时取 sv，否则取 g)；不清 sv。
func (l *Lessor) Promote(now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkTime(now); err != nil {
		return err
	}
	if l.primary {
		return ErrAlreadyPrim
	}
	for _, ls := range l.leases {
		rest := ls.g
		if ls.sv > 0 {
			rest = ls.sv
		}
		ls.x = now + l.cfg.E + rest
	}
	l.exp.rebuild()
	l.primary = true
	l.now = now
	return nil
}

// TTL 只读查询剩余寿命，不改水位 T。
// 主返回 max(x-now, 0)；从返回 sv > 0 时的 sv，否则 g。
func (l *Lessor) TTL(id, now int64) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id < 1 {
		return 0, ErrInvalidID
	}
	if err := l.checkTime(now); err != nil {
		return 0, err
	}
	ls, ok := l.leases[id]
	if !ok {
		return 0, ErrLeaseMissing
	}
	if l.primary {
		return max(ls.x-now, 0), nil
	}
	if ls.sv > 0 {
		return ls.sv, nil
	}
	return ls.g, nil
}
