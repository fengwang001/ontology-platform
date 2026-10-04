// Package transfer 实现跨服迁移的编排：迁移单、冷却、回迁。
package transfer

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/namereg"
	"ontology/shard"
)

type expEvent struct {
	expire int64
	char   string
	gen    int64
}

type expHeap []expEvent

func (h expHeap) Len() int           { return len(h) }
func (h expHeap) Less(i, j int) bool { return h[i].expire < h[j].expire }
func (h expHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *expHeap) Push(x any) { *h = append(*h, x.(expEvent)) }

func (h *expHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// 参数边界。
const (
	MinDuration = 1
	MaxDuration = 10_000_000_000
	MaxNow      = 1_000_000_000_000
	MinCap      = 1
	MaxCap      = 1_000_000
)

// 哨兵错误（含子包错误的别名）。
var (
	ErrInvalidParam = errors.New("transfer: invalid parameter")
	ErrClockRewind  = errors.New("transfer: clock rewind")
	ErrNoShard      = errors.New("transfer: shard not found")
	ErrCharExists   = errors.New("transfer: character already exists")
	ErrNoChar       = errors.New("transfer: character not found")
	ErrCapReached   = errors.New("transfer: destination load reached capacity")
	ErrOccupied     = errors.New("transfer: name is occupied")
	ErrNameReserved = errors.New("transfer: name is reserved by another character")
	ErrSameShard    = errors.New("transfer: destination is current shard")
	ErrActiveOrder  = errors.New("transfer: active transfer order already exists")
	ErrBlocked      = errors.New("transfer: character has in-transit blockers")
	ErrCoolingDown  = errors.New("transfer: character is in cooldown")
	ErrNoOrder      = errors.New("transfer: no active transfer order")
	ErrFrozen       = errors.New("transfer: character frozen by active transfer")
)

// Order 是一张迁移单。
type Order struct {
	Char     string
	Src      string
	Dst      string
	Start    int64 // 发起时刻
	Expire   int64 // Start + Wt
	IsReturn bool  // 发起时判定并冻结
}

// System 是迁移系统。
type System struct {
	mu sync.Mutex

	cd     int64
	retain int64
	win    int64
	ttl    int64
	last   int64

	shards   *shard.Registry
	names    *namereg.Registry
	chars    map[string]*character
	blockers map[string]int

	// 迁移单过期事件最小堆：惰性过期，预留按此释放。
	expiries expHeap
}

type character struct {
	name     string // 最后持有过的名字（期望名）
	home     string // 当前服
	prev     string // 上一个服（普通迁移完成写入）
	prevDone int64  // 上次普通迁移完成时刻
	coolSet  bool
	coolAt   int64 // coolStart
	rename   bool  // 待改名
	order    *Order
	orderGen int64
}

func validDur(d int64) bool   { return d >= MinDuration && d <= MaxDuration }
func validNow(now int64) bool { return now >= 0 && now <= MaxNow }
func validID(s string) bool   { return s != "" }
func validCap(c int) bool     { return c >= MinCap && c <= MaxCap }

// New 创建迁移系统。
func New(CD, R, U, Wt int64) (*System, error) {
	if !validDur(CD) || !validDur(R) || !validDur(U) || !validDur(Wt) {
		return nil, ErrInvalidParam
	}
	return &System{
		cd:       CD,
		retain:   R,
		win:      U,
		ttl:      Wt,
		shards:   shard.NewRegistry(),
		names:    namereg.NewRegistry(),
		chars:    map[string]*character{},
		blockers: map[string]int{},
	}, nil
}

// NewShard 创建一个服。
func (s *System) NewShard(sid string, cap int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(sid) || !validCap(cap) {
		return ErrInvalidParam
	}
	if _, err := s.shards.NewShard(sid, cap); err != nil {
		return ErrInvalidParam
	}
	s.names.InitShard(sid)
	return nil
}

// tick 提交逻辑时钟（仅在操作被接受后调用），并惰性提交过期迁移单。
func (s *System) tick(now int64) {
	s.last = now
	for s.expiries.Len() > 0 && s.expiries[0].expire <= now {
		ev := heap.Pop(&s.expiries).(expEvent)
		c, ok := s.chars[ev.char]
		// 事件必须仍是“当前有效单”的到期事件：作废单（取消/完成）与被新单取代的
		// 旧事件可能同样堆在堆顶，它们不得清空更新的有效单。
		if !ok || c.order == nil || c.orderGen != ev.gen || c.order.Expire != ev.expire {
			continue // 事件已作废（完成/取消/被新单替代）
		}
		// 取等即过期：过期视为已取消，预留释放、角色解冻。
		s.shards.ReleaseReserved(c.order.Dst)
		c.order = nil
	}
}

// orderActive 为纯谓词：now < Start+Wt 有效，取等即过期；不修改状态。
func (s *System) orderActive(c *character, now int64) bool {
	return c.order != nil && now < c.order.Expire
}

// effReserved 返回某服“有效在途预留”数：已到期但尚未提交的预留不计入。
// 只扫描过期堆中的到期前缀，不扫描角色表。
func (s *System) effReserved(dst string, now int64) int {
	sh, ok := s.shards.Get(dst)
	if !ok {
		return 0
	}
	expired := 0
	for _, ev := range s.expiries {
		if ev.expire > now {
			continue
		}
		c, ok := s.chars[ev.char]
		if ok && c.order != nil && c.orderGen == ev.gen && c.order.Dst == dst {
			expired++
		}
	}
	n := sh.Reserved() - expired
	if n < 0 {
		n = 0
	}
	return n
}

// effLoad 为某服的有效负载：常驻角色数 + 有效在途预留数。
func (s *System) effLoad(dst string, now int64) int {
	sh, ok := s.shards.Get(dst)
	if !ok {
		return 0
	}
	return sh.Residents() + s.effReserved(dst, now)
}

func (s *System) checkClock(now int64) error {
	if now < s.last {
		return ErrClockRewind
	}
	return nil
}

// Create 在某服创建角色并尝试持有名字。
func (s *System) Create(now int64, shardID, char, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 拒绝次序：参数非法 > 时钟回退 > 服不存在 > 角色已存在 > 负载已达 cap > 名字占用 > 名字保留。
	if !validNow(now) || !validID(shardID) || !validID(char) || !validID(name) {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	sh, ok := s.shards.Get(shardID)
	if !ok {
		return ErrNoShard
	}
	if _, ok := s.chars[char]; ok {
		return ErrCharExists
	}
	// 以下为纯判定：被拒绝不提交过期、不推进时钟。
	if s.effLoad(shardID, now) >= sh.Cap() {
		return ErrCapReached
	}
	if err := s.names.Check(now, shardID, char, name); err != nil {
		return mapNameErr(err)
	}
	// 操作接受：提交时钟与过期，再落地变更。
	s.tick(now)
	if err := s.names.Hold(now, shardID, char, name); err != nil {
		return mapNameErr(err)
	}
	if err := s.shards.AddResident(shardID); err != nil {
		// 名字判定与容量检查基于同一负载视角，理论上不可达。
		s.names.Release(shardID, char, name)
		return mapShardErr(err)
	}
	s.chars[char] = &character{name: name, home: shardID}
	return nil
}

// Request 发起迁移。
func (s *System) Request(now int64, char, dst string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 参数非法 > 时钟回退 > 角色不存在 > 服不存在 > dst 即当前服 >
	// 已有有效迁移单 > 阻断项 > 冷却中 > dst 负载已达 cap。
	if !validNow(now) || !validID(char) || !validID(dst) {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	c, ok := s.chars[char]
	if !ok {
		return ErrNoChar
	}
	if _, ok := s.shards.Get(dst); !ok {
		return ErrNoShard
	}
	if c.home == dst {
		return ErrSameShard
	}
	if s.orderActive(c, now) {
		return ErrActiveOrder
	}
	if s.blockers[char] > 0 {
		return ErrBlocked
	}
	isReturn := dst == c.prev && now < c.prevDone+s.win
	if !isReturn && c.coolSet && now < c.coolAt+s.cd {
		return ErrCoolingDown
	}
	if s.effLoad(dst, now) >= func() int {
		sh, _ := s.shards.Get(dst)
		return sh.Cap()
	}() {
		return ErrCapReached
	}
	// 操作接受：提交过期（可能解冻他人或本人），随后预留并建单。
	s.tick(now)
	if err := s.shards.Reserve(dst); err != nil {
		return ErrCapReached
	}
	c.orderGen++
	c.order = &Order{
		Char:     char,
		Src:      c.home,
		Dst:      dst,
		Start:    now,
		Expire:   now + s.ttl,
		IsReturn: isReturn,
	}
	heap.Push(&s.expiries, expEvent{expire: now + s.ttl, char: char, gen: c.orderGen})
	return nil
}

// Complete 完成迁移。
func (s *System) Complete(now int64, char string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || !validID(char) {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	c, ok := s.chars[char]
	if !ok {
		return ErrNoChar
	}
	if !s.orderActive(c, now) {
		return ErrNoOrder
	}
	s.tick(now)
	order := c.order

	// 原服角色数减 1。
	if err := s.shards.RemoveResident(order.Src); err != nil {
		return mapShardErr(err)
	}
	// 名字处理：持名则在原服进入本人保留；待改名离开不留任何保留。
	if !c.rename {
		s.names.Detain(now, s.retain, order.Src, char, c.name)
	}
	// dst 的预留转为常驻角色。
	s.shards.AdoptReserved(order.Dst)

	// 到达名字判定：期望名可用则持有并清除本人在 dst 的保留，否则待改名。
	c.home = order.Dst
	if err := s.names.Hold(now, order.Dst, char, c.name); err != nil {
		c.rename = true
	} else {
		c.rename = false
	}
	c.order = nil

	if order.IsReturn {
		// 回迁：prev 清空，coolStart 不变，因此回迁之后不能再回迁。
		c.prev = ""
		c.prevDone = 0
	} else {
		c.prev = order.Src
		c.prevDone = now
		c.coolSet = true
		c.coolAt = now
	}
	return nil
}

// Cancel 取消有效迁移单。
func (s *System) Cancel(now int64, char string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || !validID(char) {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	c, ok := s.chars[char]
	if !ok {
		return ErrNoChar
	}
	if !s.orderActive(c, now) {
		return ErrNoOrder
	}
	s.tick(now)
	dst := c.order.Dst
	c.order = nil
	s.shards.ReleaseReserved(dst)
	return nil
}

// Rename 修改角色名字。
func (s *System) Rename(now int64, char, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || !validID(char) || !validID(name) {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	c, ok := s.chars[char]
	if !ok {
		return ErrNoChar
	}
	if s.orderActive(c, now) {
		return ErrFrozen
	}
	// 纯判定：新名不可用则整体拒绝，旧名保持持有。
	if err := s.names.Check(now, c.home, char, name); err != nil {
		return mapNameErr(err)
	}
	s.tick(now)
	// 旧名立即释放且不产生保留（待改名者本就无持有）。
	if !c.rename {
		s.names.Release(c.home, char, c.name)
	}
	if err := s.names.Hold(now, c.home, char, name); err != nil {
		return mapNameErr(err)
	}
	c.name = name
	c.rename = false
	return nil
}

// SetBlockers 设置角色在途邮件/拍卖数。
func (s *System) SetBlockers(char string, n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(char) || n < 0 {
		return ErrInvalidParam
	}
	if n == 0 {
		delete(s.blockers, char)
	} else {
		s.blockers[char] = n
	}
	return nil
}

func mapNameErr(err error) error {
	switch {
	case errors.Is(err, namereg.ErrOccupied):
		return ErrOccupied
	case errors.Is(err, namereg.ErrReserved):
		return ErrNameReserved
	default:
		return ErrInvalidParam
	}
}

func mapShardErr(err error) error {
	switch {
	case errors.Is(err, shard.ErrNoShard):
		return ErrNoShard
	case errors.Is(err, shard.ErrCapReached):
		return ErrCapReached
	default:
		return ErrInvalidParam
	}
}
