// Package trade 实现玩家间虚拟物品交易的托管状态机。
//
// System 用一把互斥锁串行化全部操作，并发调用的结果等价于某个串行顺序。
// 过期采用"视图 + 懒落地"：now 大于等于到期时刻的会话在一切判定中视为已
// 取消（取等过期），但只有被接受的操作才真正释放其锁定并推进时钟；被拒绝
// 的操作不改任何可观察状态、也不推进时钟。
package trade

import (
	"container/heap"
	"errors"
	"fmt"
	"sync"

	"ontology/escrow"
	"ontology/inventory"
)

// ErrCode 标识操作的拒绝原因。
type ErrCode int

const (
	ErrInvalid      ErrCode = iota // 参数非法
	ErrClock                       // 时钟回退
	ErrNoPlayer                    // 玩家不存在
	ErrQuota                       // 开启会话数已达上限
	ErrNoSession                   // 会话不存在（含已过期、已结束）
	ErrNotMember                   // 非会话成员
	ErrInsufficient                // 可用量不足
	ErrStale                       // 版本过期
	ErrConfirmed                   // 已确认
	ErrEmpty                       // 空交易
	ErrGoldCap                     // 金币上限
	ErrSlots                       // 格数上限
)

func (c ErrCode) String() string {
	switch c {
	case ErrInvalid:
		return "invalid-argument"
	case ErrClock:
		return "clock-rollback"
	case ErrNoPlayer:
		return "player-not-found"
	case ErrQuota:
		return "session-quota-exceeded"
	case ErrNoSession:
		return "session-not-found"
	case ErrNotMember:
		return "not-a-member"
	case ErrInsufficient:
		return "insufficient"
	case ErrStale:
		return "stale-version"
	case ErrConfirmed:
		return "already-confirmed"
	case ErrEmpty:
		return "empty-trade"
	case ErrGoldCap:
		return "gold-cap-exceeded"
	case ErrSlots:
		return "slot-limit-exceeded"
	}
	return "unknown"
}

// Error 是被拒绝操作返回的错误类型，Player 指出违规主体（若有）。
type Error struct {
	Code   ErrCode
	Player string
	Msg    string
}

func (e *Error) Error() string {
	if e.Player != "" {
		return fmt.Sprintf("trade: %s (player %s): %s", e.Code, e.Player, e.Msg)
	}
	return fmt.Sprintf("trade: %s: %s", e.Code, e.Msg)
}

// CodeOf 提取错误的 ErrCode；err 为 nil 时 ok 为 false。
func CodeOf(err error) (code ErrCode, ok bool) {
	if err == nil {
		return 0, false
	}
	var ce *Error
	if errors.As(err, &ce) {
		return ce.Code, true
	}
	return 0, false
}

// ItemQty 是报价中的一种物品及数量。
type ItemQty struct {
	Item string
	Qty  int64
}

type offer struct {
	items map[string]int64
	gold  int64
}

func (o *offer) empty() bool {
	return o.gold == 0 && len(o.items) == 0
}

type session struct {
	id      int64
	a, b    string
	ver     int64
	expires int64
	offers  [2]offer // 0 为 a 的报价，1 为 b 的报价
	conf    [2]bool
	done    bool // 已成交或已取消
}

// expEntry 是到期堆中的条目；Offer 刷新到期时刻后旧条目成为垃圾，
// 弹出时与会话当前到期时刻比对即弃。
type expEntry struct {
	sid     int64
	expires int64
}

type expHeap []expEntry

func (h expHeap) Len() int { return len(h) }

func (h expHeap) Less(i, j int) bool {
	if h[i].expires != h[j].expires {
		return h[i].expires < h[j].expires
	}
	return h[i].sid < h[j].sid
}

func (h expHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *expHeap) Push(x any) { *h = append(*h, x.(expEntry)) }

func (h *expHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// System 是交易托管系统的外观，组合库存、锁定台账与会话状态机。
type System struct {
	mu      sync.Mutex
	r       int64 // 税率，千分比 0..1000
	cap     int64 // 金币上限
	slots   int64 // 种类数上限
	ttl     int64 // 会话有效期，毫秒
	quota   int64 // 每名玩家同时开启的会话数上限
	maxNow  int64 // 已接受操作的最大 now
	lastSID int64
	burned  int64 // 累计税金
	inv     *inventory.Inv
	esc     *escrow.Escrow
	sess    map[int64]*session
	open    map[string]int64 // 玩家 -> 未结束会话数（懒落地口径）
	exp     expHeap
}

// New 构造系统：r 为千分比税率（0..1000），cap 为金币上限，
// slots 为种类数上限，ttl 为会话有效期（毫秒），quota 为每玩家会话数上限。
func New(r, cap, slots, ttl, quota int64) *System {
	return &System{
		r:     r,
		cap:   cap,
		slots: slots,
		ttl:   ttl,
		quota: quota,
		inv:   inventory.New(cap, slots),
		esc:   escrow.New(),
		sess:  make(map[int64]*session),
		open:  make(map[string]int64),
	}
}

// tax 返回金额为 g 的金币流的税金：ceil(g*r/1000)，由收款方承担。
func (s *System) tax(g int64) int64 {
	return (g*s.r + 999) / 1000
}

// collectExpired 弹出所有到期时刻不大于 now 的有效会话（垃圾条目直接丢弃）。
// 返回的会话尚未落地：若操作最终被拒绝，须用 restore 放回。
func (s *System) collectExpired(now int64) []*session {
	var out []*session
	for len(s.exp) > 0 && s.exp[0].expires <= now {
		e := heap.Pop(&s.exp).(expEntry)
		sess := s.sess[e.sid]
		if sess == nil || sess.done || sess.expires != e.expires {
			continue
		}
		out = append(out, sess)
	}
	return out
}

// restore 把未落地的过期会话放回到期堆（被拒绝的操作零副作用）。
func (s *System) restore(exp []*session) {
	for _, sess := range exp {
		heap.Push(&s.exp, expEntry{sid: sess.id, expires: sess.expires})
	}
}

// materialize 落地过期会话：标记取消、释放锁定、扣减开启计数。
func (s *System) materialize(exp []*session) {
	for _, sess := range exp {
		sess.done = true
		s.esc.Release(sess.id)
		s.open[sess.a]--
		s.open[sess.b]--
	}
}

// openCount 返回玩家在 now 视角下仍开启的会话数。
func (s *System) openCount(p string, exp []*session) int64 {
	n := s.open[p]
	for _, sess := range exp {
		if sess.a == p || sess.b == p {
			n--
		}
	}
	return n
}

// expiredLocksOf 汇总即将落地会话中玩家 p 的锁定，用于 now 视角下的可用量判定。
func expiredLocksOf(exp []*session, p string) (items map[string]int64, gold int64) {
	items = make(map[string]int64)
	for _, sess := range exp {
		idx := -1
		if sess.a == p {
			idx = 0
		} else if sess.b == p {
			idx = 1
		}
		if idx < 0 {
			continue
		}
		o := &sess.offers[idx]
		for item, q := range o.items {
			items[item] += q
		}
		gold += o.gold
	}
	return items, gold
}

func mapInvErr(err error, player string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, inventory.ErrSlots):
		return &Error{Code: ErrSlots, Player: player, Msg: "grant would exceed slot limit"}
	case errors.Is(err, inventory.ErrGoldCap):
		return &Error{Code: ErrGoldCap, Player: player, Msg: "grant would exceed gold cap"}
	default:
		return &Error{Code: ErrInvalid, Msg: err.Error()}
	}
}

// Grant 发放物品并登记玩家。
func (s *System) Grant(player, item string, qty int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return mapInvErr(s.inv.Grant(player, item, qty), player)
}

// GrantGold 发放金币并登记玩家。
func (s *System) GrantGold(player string, g int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return mapInvErr(s.inv.GrantGold(player, g), player)
}

// Open 在玩家 a 与 b 之间开启会话，返回从 1 递增的会话号。
// 拒绝次序：参数非法 > 时钟回退 > 玩家不存在 > a 已达配额 > b 已达配额。
func (s *System) Open(now int64, a, b string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || a == "" || b == "" || a == b {
		return 0, &Error{Code: ErrInvalid, Msg: "open requires distinct non-empty players and now >= 0"}
	}
	if now < s.maxNow {
		return 0, &Error{Code: ErrClock, Msg: "now is before the max accepted now"}
	}
	exp := s.collectExpired(now)
	reject := func(err error) (int64, error) {
		s.restore(exp)
		return 0, err
	}
	if !s.inv.Has(a) {
		return reject(&Error{Code: ErrNoPlayer, Player: a, Msg: "player a is not registered"})
	}
	if !s.inv.Has(b) {
		return reject(&Error{Code: ErrNoPlayer, Player: b, Msg: "player b is not registered"})
	}
	if s.openCount(a, exp) >= s.quota {
		return reject(&Error{Code: ErrQuota, Player: a, Msg: "player a has too many open sessions"})
	}
	if s.openCount(b, exp) >= s.quota {
		return reject(&Error{Code: ErrQuota, Player: b, Msg: "player b has too many open sessions"})
	}
	s.materialize(exp)
	s.maxNow = now
	s.lastSID++
	sess := &session{id: s.lastSID, a: a, b: b, expires: now + s.ttl}
	s.sess[sess.id] = sess
	s.open[a]++
	s.open[b]++
	heap.Push(&s.exp, expEntry{sid: sess.id, expires: sess.expires})
	return sess.id, nil
}

// lookup 返回 now 视角下仍存活的会话；不存在、已结束或已过期时返回 nil。
func (s *System) lookup(sid, now int64) *session {
	sess := s.sess[sid]
	if sess == nil || sess.done || now >= sess.expires {
		return nil
	}
	return sess
}

func memberIndex(sess *session, who string) int {
	if who == sess.a {
		return 0
	}
	if who == sess.b {
		return 1
	}
	return -1
}

// Offer 整体替换 who 在会话 sid 中的报价。成功时本会话锁定改为新报价、
// ver 加 1、双方确认清空、到期时刻刷新为 now+ttl（相同报价亦如此）。
// 拒绝次序：参数非法 > 时钟回退 > 会话不存在 > 非会话成员 > 不足。
func (s *System) Offer(now, sid int64, who string, items []ItemQty, gold int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	newItems := make(map[string]int64, len(items))
	valid := now >= 0 && sid >= 1 && who != "" && gold >= 0 && gold <= s.cap && len(items) <= 16
	if valid {
		for _, it := range items {
			if it.Item == "" || it.Qty < 1 || it.Qty > 1_000_000_000 {
				valid = false
				break
			}
			if _, dup := newItems[it.Item]; dup {
				valid = false
				break
			}
			newItems[it.Item] = it.Qty
		}
	}
	if !valid {
		return &Error{Code: ErrInvalid, Msg: "offer requires 0..16 distinct items of qty 1..1e9 and gold 0..CAP"}
	}
	if now < s.maxNow {
		return &Error{Code: ErrClock, Msg: "now is before the max accepted now"}
	}
	exp := s.collectExpired(now)
	reject := func(err error) error {
		s.restore(exp)
		return err
	}
	sess := s.lookup(sid, now)
	if sess == nil {
		return reject(&Error{Code: ErrNoSession, Msg: "session is missing, finished or expired"})
	}
	idx := memberIndex(sess, who)
	if idx < 0 {
		return reject(&Error{Code: ErrNotMember, Player: who, Msg: "not a member of the session"})
	}
	// 可用量 = 持有量 - 本人在其他存活会话中的锁定量。
	// 台账合计包含本会话当前报价与待落地过期会话的锁定，两者都要剔除。
	expItems, expGold := expiredLocksOf(exp, who)
	cur := &sess.offers[idx]
	for item, q := range newItems {
		lockedOthers := s.esc.Locked(who, item) - expItems[item] - cur.items[item]
		if avail := s.inv.Holding(who, item) - lockedOthers; q > avail {
			return reject(&Error{Code: ErrInsufficient, Player: who,
				Msg: fmt.Sprintf("item %s: need %d, available %d", item, q, avail)})
		}
	}
	lockedGoldOthers := s.esc.LockedGold(who) - expGold - cur.gold
	if avail := s.inv.Gold(who) - lockedGoldOthers; gold > avail {
		return reject(&Error{Code: ErrInsufficient, Player: who,
			Msg: fmt.Sprintf("gold: need %d, available %d", gold, avail)})
	}
	s.materialize(exp)
	s.maxNow = now
	s.esc.Set(sid, who, newItems, gold)
	sess.offers[idx] = offer{items: newItems, gold: gold}
	sess.ver++
	sess.conf[0], sess.conf[1] = false, false
	sess.expires = now + s.ttl
	heap.Push(&s.exp, expEntry{sid: sid, expires: sess.expires})
	return nil
}

// mergeDeltas 把 remove（负）与 add（正）两份报价合成一份持有量调整。
func mergeDeltas(remove, add map[string]int64) map[string]int64 {
	d := make(map[string]int64, len(remove)+len(add))
	for item, q := range remove {
		d[item] -= q
	}
	for item, q := range add {
		d[item] += q
	}
	return d
}

// Confirm 确认当前版本的报价；对方已确认时本次确认促成成交。
// 拒绝次序：参数非法 > 时钟回退 > 会话不存在 > 非会话成员 > 版本过期 >
// 已确认 > 空交易 > 金币上限（先 a 后 b）> 格数上限（先 a 后 b）。
// Confirm 不刷新到期时刻；检查失败时本次确认不记，会话其余状态不变。
func (s *System) Confirm(now, sid int64, who string, ver int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || sid < 1 || who == "" || ver < 0 {
		return &Error{Code: ErrInvalid, Msg: "confirm requires sid >= 1, non-empty member and ver >= 0"}
	}
	if now < s.maxNow {
		return &Error{Code: ErrClock, Msg: "now is before the max accepted now"}
	}
	exp := s.collectExpired(now)
	reject := func(err error) error {
		s.restore(exp)
		return err
	}
	sess := s.lookup(sid, now)
	if sess == nil {
		return reject(&Error{Code: ErrNoSession, Msg: "session is missing, finished or expired"})
	}
	idx := memberIndex(sess, who)
	if idx < 0 {
		return reject(&Error{Code: ErrNotMember, Player: who, Msg: "not a member of the session"})
	}
	if ver != sess.ver {
		return reject(&Error{Code: ErrStale, Msg: fmt.Sprintf("ver %d != current ver %d", ver, sess.ver)})
	}
	if sess.conf[idx] {
		return reject(&Error{Code: ErrConfirmed, Player: who, Msg: "already confirmed at this version"})
	}
	if sess.offers[0].empty() && sess.offers[1].empty() {
		return reject(&Error{Code: ErrEmpty, Msg: "both offers are empty"})
	}
	if !sess.conf[1-idx] {
		// 对方尚未确认：仅记录本次确认。
		s.materialize(exp)
		s.maxNow = now
		sess.conf[idx] = true
		return nil
	}
	// 本次确认将促成成交：先查金币上限（先 a 后 b），再查格数上限（先 a 后 b）。
	oA, oB := &sess.offers[0], &sess.offers[1]
	taxA := s.tax(oA.gold) // a 付出的金币，b 实收 oA.gold - taxA
	taxB := s.tax(oB.gold) // b 付出的金币，a 实收 oB.gold - taxB
	if after := s.inv.Gold(sess.a) - oA.gold + oB.gold - taxB; after > s.cap {
		return reject(&Error{Code: ErrGoldCap, Player: sess.a,
			Msg: fmt.Sprintf("player a gold after trade %d exceeds cap %d", after, s.cap)})
	}
	if after := s.inv.Gold(sess.b) - oB.gold + oA.gold - taxA; after > s.cap {
		return reject(&Error{Code: ErrGoldCap, Player: sess.b,
			Msg: fmt.Sprintf("player b gold after trade %d exceeds cap %d", after, s.cap)})
	}
	deltaA := mergeDeltas(oA.items, oB.items)
	if kinds := s.inv.KindsAfter(sess.a, deltaA); kinds > s.slots {
		return reject(&Error{Code: ErrSlots, Player: sess.a,
			Msg: fmt.Sprintf("player a kinds after trade %d exceeds slots %d", kinds, s.slots)})
	}
	deltaB := mergeDeltas(oB.items, oA.items)
	if kinds := s.inv.KindsAfter(sess.b, deltaB); kinds > s.slots {
		return reject(&Error{Code: ErrSlots, Player: sess.b,
			Msg: fmt.Sprintf("player b kinds after trade %d exceeds slots %d", kinds, s.slots)})
	}
	// 检查通过：原子交换、释放本会话锁定并结束会话。
	s.materialize(exp)
	s.maxNow = now
	s.inv.ApplyItems(sess.a, deltaA)
	s.inv.ApplyItems(sess.b, deltaB)
	s.inv.ApplyGold(sess.a, -oA.gold+oB.gold-taxB)
	s.inv.ApplyGold(sess.b, -oB.gold+oA.gold-taxA)
	s.burned += taxA + taxB
	sess.done = true
	s.esc.Release(sid)
	s.open[sess.a]--
	s.open[sess.b]--
	return nil
}

// Burned 返回累计税金（已销毁金币）。
func (s *System) Burned() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.burned
}

// Gold 返回玩家当前金币。
func (s *System) Gold(player string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inv.Gold(player)
}

// Holding 返回玩家某物品的当前持有量。
func (s *System) Holding(player, item string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inv.Holding(player, item)
}

// Holdings 返回玩家全部持有量的副本。
func (s *System) Holdings(player string) map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inv.Holdings(player)
}

// Kinds 返回玩家持有量大于 0 的物品种类数。
func (s *System) Kinds(player string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inv.Kinds(player)
}

// Locked 返回玩家某物品在所有有效会话中的锁定合计（含待落地的过期会话）。
func (s *System) Locked(player, item string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.esc.Locked(player, item)
}

// LockedGold 返回玩家金币的锁定合计（含待落地的过期会话）。
func (s *System) LockedGold(player string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.esc.LockedGold(player)
}

// MaxNow 返回已接受操作的最大 now。
func (s *System) MaxNow() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxNow
}

// Touched 返回 escrow 内部非导出计数器的当前值。
func (s *System) Touched() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.esc.Touched()
}

// Cancel 由任一成员取消会话并释放其全部锁定。
// 拒绝次序：参数非法 > 时钟回退 > 会话不存在 > 非会话成员。
func (s *System) Cancel(now, sid int64, who string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || sid < 1 || who == "" {
		return &Error{Code: ErrInvalid, Msg: "cancel requires sid >= 1 and non-empty member"}
	}
	if now < s.maxNow {
		return &Error{Code: ErrClock, Msg: "now is before the max accepted now"}
	}
	exp := s.collectExpired(now)
	reject := func(err error) error {
		s.restore(exp)
		return err
	}
	sess := s.lookup(sid, now)
	if sess == nil {
		return reject(&Error{Code: ErrNoSession, Msg: "session is missing, finished or expired"})
	}
	if memberIndex(sess, who) < 0 {
		return reject(&Error{Code: ErrNotMember, Player: who, Msg: "not a member of the session"})
	}
	s.materialize(exp)
	s.maxNow = now
	sess.done = true
	s.esc.Release(sid)
	s.open[sess.a]--
	s.open[sess.b]--
	return nil
}
