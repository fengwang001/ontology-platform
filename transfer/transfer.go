// Package transfer 实现游戏角色跨服迁移：迁移单、冷却、回迁与名字交接。
// System 用单互斥锁串行化所有操作，并发调用等价于某个串行顺序；
// 判定路径不迭代 map，相同操作序列重放结果相同。
package transfer

import (
	"errors"
	"sync"

	"ontology/namereg"
	"ontology/shard"
)

// 各类拒绝原因，均可用 errors.Is 判定。
var (
	ErrInvalidParam  = errors.New("invalid parameter")
	ErrClockRollback = errors.New("clock rollback")
	ErrShardNotFound = errors.New("shard not found")
	ErrShardExists   = errors.New("shard already exists")
	ErrCharNotFound  = errors.New("char not found")
	ErrCharExists    = errors.New("char already exists")
	ErrShardFull     = errors.New("shard load at capacity")
	ErrNameOccupied  = errors.New("name occupied")
	ErrNameReserved  = errors.New("name reserved")
	ErrSameShard     = errors.New("destination is current shard")
	ErrTicketExists  = errors.New("active transfer ticket exists")
	ErrBlocked       = errors.New("blocking mail or auction in flight")
	ErrCooling       = errors.New("transfer cooling down")
	ErrNoTicket      = errors.New("no active transfer")
	ErrFrozen        = errors.New("char frozen by active transfer")
)

const (
	maxParam = int64(10_000_000_000)    // CD/R/U/Wt 上限：1e10 毫秒
	maxNow   = int64(1_000_000_000_000) // now 上限：1e12 毫秒
	maxCap   = 1_000_000                // 服容量上限：1e6
)

// ticket 是一张迁移单；done 表示已完结（完成/取消/过期），
// 供过期队列跳过重复释放。
type ticket struct {
	dst       int64
	reqAt     int64
	returning bool // 发起时判定的回迁标记
	done      bool
}

type char struct {
	shard    int64
	name     string // 当前持有的名字，"" 表示未持有
	lastName string // 最后持有过的名字（期望名）
	pending  bool   // 待改名
	prev     int64  // 上一个服
	hasPrev  bool
	prevDone int64
	cool     int64
	hasCool  bool
	blockers int
	tk       *ticket
}

// expiryItem 是过期队列中的一项；now 单调不减保证入队时刻有序。
type expiryItem struct {
	at int64
	tk *ticket
}

// System 是迁移系统门面，组合 shard、namereg 与迁移规则。
type System struct {
	mu      sync.Mutex
	cd      int64 // 迁移冷却
	r       int64 // 名字保留期
	u       int64 // 回迁窗口
	wt      int64 // 迁移单有效期
	maxNow  int64
	hasNow  bool
	shards  *shard.Registry
	names   *namereg.NameReg
	chars   map[string]*char
	expiryQ []expiryItem
}

// New 校验四个时长参数（均在 [1, 1e10] 毫秒）并创建系统。
func New(cd, r, u, wt int64) (*System, error) {
	for _, v := range []int64{cd, r, u, wt} {
		if v < 1 || v > maxParam {
			return nil, ErrInvalidParam
		}
	}
	return &System{
		cd: cd, r: r, u: u, wt: wt,
		shards: shard.NewRegistry(),
		names:  namereg.New(),
		chars:  make(map[string]*char),
	}, nil
}

// NewShard 注册一个服，容量 cap 须在 [1, 1e6]。
func (s *System) NewShard(sid int64, cap int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cap < 1 || cap > maxCap {
		return ErrInvalidParam
	}
	if !s.shards.Add(sid, cap) {
		return ErrShardExists
	}
	s.names.AddShard(sid)
	return nil
}

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// clockOK 拒绝时钟回退：now 不得小于已接受操作的最大 now。
func (s *System) clockOK(now int64) error {
	if s.hasNow && now < s.maxNow {
		return ErrClockRollback
	}
	return nil
}

// accept 推进已接受操作的时钟水位。
func (s *System) accept(now int64) {
	if !s.hasNow || now > s.maxNow {
		s.maxNow, s.hasNow = now, true
	}
}

// drain 惰性回收已到期的在途预留（取等即过期）；摊还 O(1)，不清扫。
// 只允许在被接受的操作里调用：被拒绝的操作不得改变可观察状态。
func (s *System) drain(now int64) {
	for len(s.expiryQ) > 0 && s.expiryQ[0].at <= now {
		it := s.expiryQ[0]
		s.expiryQ = s.expiryQ[1:]
		if !it.tk.done {
			it.tk.done = true
			s.shards.Release(it.tk.dst)
		}
	}
}

// ticketValid 是迁移单有效性的纯判定（now < reqAt+wt 且未完结），不改状态。
func (s *System) ticketValid(c *char, now int64) bool {
	return c.tk != nil && !c.tk.done && now < c.tk.reqAt+s.wt
}

// expiredReserved 统计各服已到期但尚未物理回收的在途预留数（now 视角），
// 供被拒绝路径上的负载判定使用；队列按到期时刻有序，只扫描前缀。
func (s *System) expiredReserved(now int64) map[int64]int {
	var m map[int64]int
	for _, it := range s.expiryQ {
		if it.at > now {
			break
		}
		if !it.tk.done {
			if m == nil {
				m = make(map[int64]int)
			}
			m[it.tk.dst]++
		}
	}
	return m
}

// loadAt 返回 sid 服在 now 视角下的有效负载。
func (s *System) loadAt(sid int64, expired map[int64]int) int {
	return s.shards.Load(sid) - expired[sid]
}

// Create 在 sid 服创建角色 char 并持有 name。
// 拒绝次序：参数非法 > 时钟回退 > 服不存在 > 角色已存在 > 负载已达 cap >
// 名字被占用 > 名字保留中。
func (s *System) Create(now, sid int64, charID, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || charID == "" || name == "" {
		return ErrInvalidParam
	}
	if err := s.clockOK(now); err != nil {
		return err
	}
	if !s.shards.Exists(sid) {
		return ErrShardNotFound
	}
	if _, ok := s.chars[charID]; ok {
		return ErrCharExists
	}
	if s.loadAt(sid, s.expiredReserved(now)) >= s.shards.Cap(sid) {
		return ErrShardFull
	}
	occupied, reserved := s.names.Status(sid, name, charID, now)
	if occupied {
		return ErrNameOccupied
	}
	if reserved {
		return ErrNameReserved
	}
	s.drain(now)
	s.shards.AddChar(sid)
	s.names.Hold(sid, name, charID)
	s.chars[charID] = &char{shard: sid, name: name, lastName: name}
	s.accept(now)
	return nil
}

// Request 为 char 发起到 dst 的迁移：成功则占用 dst 一个在途预留并冻结角色。
// 拒绝次序：参数非法 > 时钟回退 > 角色不存在 > 服不存在 > dst 即当前服 >
// 已有有效迁移单 > 阻断项 > 冷却中 > dst 负载已达 cap。
func (s *System) Request(now int64, charID string, dst int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || charID == "" {
		return ErrInvalidParam
	}
	if err := s.clockOK(now); err != nil {
		return err
	}
	c, ok := s.chars[charID]
	if !ok {
		return ErrCharNotFound
	}
	if !s.shards.Exists(dst) {
		return ErrShardNotFound
	}
	if dst == c.shard {
		return ErrSameShard
	}
	if s.ticketValid(c, now) {
		return ErrTicketExists
	}
	if c.blockers > 0 {
		return ErrBlocked
	}
	returning := c.hasPrev && dst == c.prev && now < c.prevDone+s.u
	if !returning && c.hasCool && now < c.cool+s.cd {
		return ErrCooling
	}
	if s.loadAt(dst, s.expiredReserved(now)) >= s.shards.Cap(dst) {
		return ErrShardFull
	}
	s.drain(now)
	if !s.shards.Reserve(dst) {
		return ErrShardFull
	}
	c.tk = &ticket{dst: dst, reqAt: now, returning: returning}
	s.expiryQ = append(s.expiryQ, expiryItem{at: now + s.wt, tk: c.tk})
	s.accept(now)
	return nil
}

// Complete 完成 char 的有效迁移单，否则报无在途迁移。
func (s *System) Complete(now int64, charID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || charID == "" {
		return ErrInvalidParam
	}
	if err := s.clockOK(now); err != nil {
		return err
	}
	c, ok := s.chars[charID]
	if !ok {
		return ErrCharNotFound
	}
	if !s.ticketValid(c, now) {
		return ErrNoTicket
	}
	tk := c.tk
	s.drain(now) // tk 未到期，不受回收影响
	src := c.shard
	if c.name != "" {
		// 持有的名字在原服进入保留，保留者为本人；待改名者不持有名字，不留保留。
		s.names.Release(src, charID)
		s.names.Reserve(src, c.name, charID, now+s.r)
	}
	s.shards.Depart(src)
	s.shards.Arrive(tk.dst)
	c.shard = tk.dst
	c.tk = nil
	tk.done = true
	if desired := c.lastName; desired != "" {
		occupied, reserved := s.names.Status(tk.dst, desired, charID, now)
		if !occupied && !reserved {
			s.names.Hold(tk.dst, desired, charID) // 同时清除本人在 dst 的该名保留
			c.name, c.pending = desired, false
		} else {
			c.name, c.pending = "", true
		}
	} else {
		c.name, c.pending = "", true
	}
	if tk.returning {
		c.hasPrev = false // 回迁完成：prev 清空、coolStart 不变，不可连环回迁
	} else {
		c.prev, c.hasPrev, c.prevDone = src, true, now
		c.cool, c.hasCool = now, true
	}
	s.accept(now)
	return nil
}

// Cancel 取消 char 的有效迁移单并释放 dst 的在途预留。
func (s *System) Cancel(now int64, charID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || charID == "" {
		return ErrInvalidParam
	}
	if err := s.clockOK(now); err != nil {
		return err
	}
	c, ok := s.chars[charID]
	if !ok {
		return ErrCharNotFound
	}
	if !s.ticketValid(c, now) {
		return ErrNoTicket
	}
	tk := c.tk
	s.drain(now)
	s.shards.Release(tk.dst)
	tk.done = true
	c.tk = nil
	s.accept(now)
	return nil
}

// Rename 为 char 改名：有效迁移单存续期间报已冻结；
// 其后依次为名字被占用 > 名字保留中。成功立即释放旧名（不产生保留）。
func (s *System) Rename(now int64, charID, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || charID == "" || name == "" {
		return ErrInvalidParam
	}
	if err := s.clockOK(now); err != nil {
		return err
	}
	c, ok := s.chars[charID]
	if !ok {
		return ErrCharNotFound
	}
	if s.ticketValid(c, now) {
		return ErrFrozen
	}
	occupied, reserved := s.names.Status(c.shard, name, charID, now)
	if occupied {
		return ErrNameOccupied
	}
	if reserved {
		return ErrNameReserved
	}
	s.drain(now)
	if c.name != "" {
		s.names.Release(c.shard, charID) // 旧名立即释放，不产生保留
	}
	s.names.Hold(c.shard, name, charID)
	c.name, c.lastName, c.pending = name, name, false
	s.accept(now)
	return nil
}

// SetBlockers 设定 char 的在途邮件与拍卖数（阻断项）。
func (s *System) SetBlockers(charID string, n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if charID == "" || n < 0 {
		return ErrInvalidParam
	}
	c, ok := s.chars[charID]
	if !ok {
		return ErrCharNotFound
	}
	c.blockers = n
	return nil
}

// CharView 是角色的只读快照。
type CharView struct {
	Shard         int64
	Name          string
	PendingRename bool
}

// Inspect 返回角色快照；角色不存在时 ok 为 false。
func (s *System) Inspect(charID string) (view CharView, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chars[charID]
	if !ok {
		return CharView{}, false
	}
	return CharView{Shard: c.shard, Name: c.name, PendingRename: c.pending}, true
}

// ShardLoad 返回服的当前负载（角色数 + 在途预留数）。
func (s *System) ShardLoad(sid int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shards.Load(sid)
}

// NameProbes 返回名字判定累计触达的记录数（供测试验证常数上界）。
func (s *System) NameProbes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.names.Probes()
}

// ResetNameProbes 清零名字判定 probes 计数器。
func (s *System) ResetNameProbes() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.names.ResetProbes()
}
