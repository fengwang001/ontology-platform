// Package session 实现在线计费的会话、请求序号、用量结算与配额授予引擎。
package session

import (
	"errors"
	"sort"
	"sync"

	"ontology/balance"
	"ontology/rating"
)

// 哨兵错误：拒绝次序为 参数非法 > 时钟回退 > 会话不存在
// （Open 为 会话已存在 > 会话超限）> 序号错 > 无授予记录而 used>0。
var (
	ErrInvalid     = errors.New("invalid argument")
	ErrClockRewind = errors.New("clock rewind")
	ErrNoSession   = errors.New("session not found")
	ErrExists      = errors.New("session already exists")
	ErrSessionCap  = errors.New("session limit exceeded")
	ErrSeq         = errors.New("bad sequence number")
	ErrNoGrant     = errors.New("no grant record for rate group")
)

// Reply 是 Update / Close 成功执行（或其精确重传）后的应答。
type Reply struct {
	Charged    int64
	Unbilled   int64
	Granted    int64
	Final      bool
	Denied     bool
	ValidUntil int64
}

// grant 是属于（会话，rg）的授予记录。res 为账本中的在途预留，
// 到期后 res 不再占用但记录保留，直到下一次上报结算。
type grant struct {
	rg  int64
	g   int64
	r   int64
	e   int64
	res *balance.Reservation
}

type sessState struct {
	name     string
	acct     string
	lastSeq  int64
	unbilled int64
	grants   map[int64]*grant // rg → 授予记录，至多一条
	lastRep  *Reply           // 上一已处理请求（Update/Close）的应答，供重传原样返回
}

// Engine 是配额授予与回收引擎。
type Engine struct {
	smax int64
	lmin int64
	v    int64
	nmax int

	ledger *balance.Ledger
	rates  *rating.Table

	mu      sync.RWMutex
	lastNow int64

	sessions map[string]*sessState
	acctSess map[string]int

	// touched 为非导出计数器：上一次成功 Update 考察的授予记录数。
	// Update 仅按 (会话,rg) 直接定位至多 1 条记录，不扫描任何账户/会话。
	touched int
}

// New 创建引擎。参数非法时 panic（构造函数别无错误通道）。
func New(Smax, Lmin, V int64, Nmax int) *Engine {
	if Lmin < 1 || Smax < Lmin || Smax > 1_000_000 ||
		V < 1 || V > 1_000_000_000 || Nmax < 1 {
		panic(ErrInvalid)
	}
	return &Engine{
		smax:     Smax,
		lmin:     Lmin,
		v:        V,
		nmax:     Nmax,
		ledger:   balance.NewLedger(),
		rates:    rating.NewTable(),
		sessions: make(map[string]*sessState),
		acctSess: make(map[string]int),
	}
}

func validNow(now int64) bool  { return now >= 0 && now <= 1_000_000_000_000 }
func validAmount(a int64) bool { return a >= 1 && a <= 1_000_000_000_000 }
func validRG(rg int64) bool    { return rg >= 1 && rg <= 1000 }
func validPrice(p int64) bool  { return p >= 1 && p <= 1_000_000 }
func validUsage(x int64) bool  { return x >= 0 && x <= 1_000_000_000 }
func validN(n int64) bool      { return n >= 1 }

// SetRate 设置费率组单价。
func (e *Engine) SetRate(rg, price, now int64) error {
	if !validRG(rg) || !validPrice(price) || !validNow(now) {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.lastNow {
		return ErrClockRewind
	}
	e.rates.Set(rg, price, now)
	e.lastNow = now
	return nil
}

// TopUp 给账户加款（账户首次出现即建立，余额从 0 起）。
func (e *Engine) TopUp(acct string, amount, now int64) error {
	if !validAmount(amount) || !validNow(now) {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.lastNow {
		return ErrClockRewind
	}
	e.ledger.TopUp(acct, amount)
	e.lastNow = now
	return nil
}

// Open 建立会话。
func (e *Engine) Open(sess, acct string, now int64) error {
	if !validNow(now) {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.lastNow {
		return ErrClockRewind
	}
	if _, ok := e.sessions[sess]; ok {
		return ErrExists
	}
	if e.acctSess[acct] >= e.nmax {
		return ErrSessionCap
	}
	e.ledger.Ensure(acct) // 账户在 Open 中首次出现即建立
	e.sessions[sess] = &sessState{
		name:   sess,
		acct:   acct,
		grants: make(map[int64]*grant),
	}
	e.acctSess[acct]++
	e.lastNow = now
	return nil
}

// settle 对一条授予记录做结算：先释放其未到期预留，再按授予时单价 r 从 free 扣费，
// 扣不动的用量累加未计费，最后删除记录。返回（扣费单位数, 新增未计费单位数）。
func (e *Engine) settle(s *sessState, gr *grant, used, now int64) (charged, unbilled int64) {
	e.ledger.Release(gr.res, now) // 已到期则释放金额为 0
	if used > 0 {
		free := e.ledger.Free(s.acct, now)
		afford := free / gr.r
		c := used
		if c > afford {
			c = afford
		}
		if c > 0 {
			if !e.ledger.Charge(s.acct, c*gr.r) {
				c = 0 // 理论不可达：c*r ≤ free
			}
		}
		charged = c
		unbilled = used - c
		s.unbilled += unbilled
	}
	e.ledger.Forget(gr.res)
	delete(s.grants, gr.rg)
	return charged, unbilled
}

// Update 执行一次 结算 + 授予。
func (e *Engine) Update(sess string, n, rg, used, want, now int64) (Reply, error) {
	if !validN(n) || !validRG(rg) || !validUsage(used) || !validUsage(want) || !validNow(now) {
		return Reply{}, ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.sessions[sess]
	if !ok {
		return Reply{}, ErrNoSession
	}
	if n == s.lastSeq && s.lastRep != nil { // 重传：原样返回，不推进时钟
		return *s.lastRep, nil
	}
	// 使用未设单价的 rg 属参数非法（结算与授予都需要单价）。
	if price, ok := e.rates.Get(rg); !ok || !validPrice(price) {
		return Reply{}, ErrInvalid
	}
	if now < e.lastNow {
		return Reply{}, ErrClockRewind
	}
	if n != s.lastSeq+1 {
		return Reply{}, ErrSeq
	}

	e.touched = 0
	var rep Reply
	gr := s.grants[rg] // 至多 1 条：本次考察的唯一授予记录
	e.touched++
	if gr != nil {
		rep.Charged, rep.Unbilled = e.settle(s, gr, used, now)
	} else if used > 0 {
		return Reply{}, ErrNoGrant // 无记录时 used 须为 0
	}

	if want > 0 {
		r, _ := e.rates.Get(rg) // 已在锁外校验存在
		free := e.ledger.Free(s.acct, now)
		u := free / r
		g0 := want
		if e.smax < g0 {
			g0 = e.smax
		}
		if u < g0 {
			g0 = u
		}
		d := u - g0
		g := g0
		if d > 0 && d < e.lmin {
			g = u // 吃掉零头：可超过 want 与 Smax
		}
		if g > 0 {
			res := e.ledger.Hold(s.acct, g, r, now+e.v)
			s.grants[rg] = &grant{rg: rg, g: g, r: r, e: now + e.v, res: res}
			rep.ValidUntil = now + e.v
		}
		rep.Granted = g
		rep.Final = g == u
		rep.Denied = g == 0
	}

	s.lastSeq = n
	s.lastRep = &rep
	e.lastNow = now
	return rep, nil
}

// Close 按升序对列出的每个 rg 结算；未列出而有记录的 rg 按 used=0 结算；
// 列出无记录且 used>0 的 rg 则整体拒绝。成功后删除会话。
func (e *Engine) Close(sess string, n int64, usedByRg map[int64]int64, now int64) (Reply, error) {
	if !validN(n) || !validNow(now) {
		return Reply{}, ErrInvalid
	}
	for rg, used := range usedByRg {
		if !validRG(rg) || !validUsage(used) {
			return Reply{}, ErrInvalid
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.sessions[sess]
	if !ok {
		return Reply{}, ErrNoSession
	}
	if n == s.lastSeq && s.lastRep != nil {
		return *s.lastRep, nil
	}
	if now < e.lastNow {
		return Reply{}, ErrClockRewind
	}
	if n != s.lastSeq+1 {
		return Reply{}, ErrSeq
	}
	// 先做全部前置校验，任一不满足则整体拒绝、不落任何状态。
	for rg, used := range usedByRg {
		if used > 0 && s.grants[rg] == nil {
			return Reply{}, ErrNoGrant
		}
	}

	var rep Reply
	rgs := make([]int64, 0, len(s.grants))
	for rg := range s.grants {
		rgs = append(rgs, rg)
	}
	sort.Slice(rgs, func(i, j int) bool { return rgs[i] < rgs[j] })
	for _, rg := range rgs {
		c, u := e.settle(s, s.grants[rg], usedByRg[rg], now)
		rep.Charged += c
		rep.Unbilled += u
	}

	delete(e.sessions, sess)
	e.acctSess[s.acct]--
	e.lastNow = now
	// 不缓存 Close 应答：会话已不存在，其重传报会话不存在。
	return rep, nil
}

// Touched 返回上一次成功 Update 考察的授予记录数（证明/对照用）。
func (e *Engine) Touched() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.touched
}
