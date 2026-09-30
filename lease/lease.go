// Package lease 实现整数地址池租约服务器。
//
// 服务器从闭区间地址池中向客户端提供发现（暂留）、确认（含续租）、
// 释放与拒收四种操作。同一地址任一时刻至多存在一份有效租约，
// 且有效租约不与其他客户端的有效暂留并存。
//
// 时间规则（均为左闭右开区间）：
//   - 租约：[确认时刻, 确认时刻+L)
//   - 暂留：[发现时刻, 发现时刻+H)
//   - 隔离：[拒收时刻, 拒收时刻+Q)
//
// 地址空闲当且仅当：无有效租约、无有效暂留、不在隔离期内。
//
// 所有操作可并发调用；操作显式携带时间戳，相同的操作序列
// 重放结果完全相同。
package lease

import (
	"errors"
	"sync"
	"time"
)

// 操作被拒绝的原因。被拒绝的操作不改变任何状态。
var (
	// ErrClockRollback 表示操作时间早于服务器已见过的最新时刻。
	ErrClockRollback = errors.New("时钟回拨")
	// ErrEmptyClient 表示发现操作的客户端标识为空。
	ErrEmptyClient = errors.New("客户端为空")
	// ErrAddrOutOfPool 表示地址不在地址池闭区间内。
	ErrAddrOutOfPool = errors.New("地址不在池内")
	// ErrPoolExhausted 表示按优先次序找不到任何可用地址。
	ErrPoolExhausted = errors.New("池已耗尽")
	// ErrAddrBusy 表示地址被其他客户端持有租约或暂留。
	ErrAddrBusy = errors.New("地址被他人持有或暂留")
	// ErrNoHoldOrLease 表示该客户端在此地址上既无有效暂留也无有效租约。
	ErrNoHoldOrLease = errors.New("该客户端无有效暂留或租约")
	// ErrNotHolder 表示客户端不是该地址的持有者（拒收时含暂留者）。
	ErrNotHolder = errors.New("非持有者")
)

// Config 是租约服务器的参数。
type Config struct {
	// Lo、Hi 为地址池闭区间 [Lo, Hi]，要求 Lo <= Hi。
	Lo, Hi int
	// Lease 为租期 L。
	Lease time.Duration
	// Hold 为暂留时长 H。
	Hold time.Duration
	// Quarantine 为拒收隔离时长 Q。
	Quarantine time.Duration
}

// addrState 记录单个地址的状态。
type addrState struct {
	leaseClient string    // 当前租约持有者，空表示无有效租约
	leaseEnd    time.Time // 租约终点（左闭右开的右端）
	holdClient  string    // 当前暂留者，空表示无有效暂留
	holdEnd     time.Time // 暂留终点
	quarUntil   time.Time // 隔离终点
	everLeased  bool      // 是否曾经确认过租约
	termAt      time.Time // 最近一次租约终止时刻
}

// Server 是地址租约服务器，可并发使用。
type Server struct {
	mu       sync.Mutex
	lo       int
	hi       int
	lease    time.Duration
	hold     time.Duration
	quar     time.Duration
	last     time.Time // 已见过的最新时刻，用于检测时钟回拨
	hasLast  bool
	addrs    []addrState // 下标 = 地址 - lo
	lastHeld map[string]int
}

// NewServer 按给定参数创建租约服务器。
func NewServer(cfg Config) *Server {
	if cfg.Hi < cfg.Lo {
		panic("lease: 地址池区间为空")
	}
	return &Server{
		lo:       cfg.Lo,
		hi:       cfg.Hi,
		lease:    cfg.Lease,
		hold:     cfg.Hold,
		quar:     cfg.Quarantine,
		addrs:    make([]addrState, cfg.Hi-cfg.Lo+1),
		lastHeld: make(map[string]int),
	}
}

func (s *Server) inPool(addr int) bool {
	return addr >= s.lo && addr <= s.hi
}

// normalize 将 now 时刻已自然到期的租约与暂留物化清除。
// 租约过期时，其终止时刻取租约终点。该转换不改变任何可观察行为，
// 因为过期租约/暂留在任何后续判定中本就无效。
func (s *Server) normalize(now time.Time) {
	for i := range s.addrs {
		st := &s.addrs[i]
		if st.leaseClient != "" && !now.Before(st.leaseEnd) {
			st.termAt = st.leaseEnd
			st.leaseClient = ""
			st.leaseEnd = time.Time{}
		}
		if st.holdClient != "" && !now.Before(st.holdEnd) {
			st.holdClient = ""
			st.holdEnd = time.Time{}
		}
	}
}

// free 报告地址在 now 时刻是否空闲：无有效租约、无有效暂留、不在隔离期。
func (s *Server) free(idx int, now time.Time) bool {
	st := &s.addrs[idx]
	return st.leaseClient == "" && st.holdClient == "" && !now.Before(st.quarUntil)
}

// lessTerm 比较两个空闲地址的选址优先级：
// 租约终止时刻最早者优先，从未有过租约者视为最早。
// 完全并列时返回 false，由调用方按地址升序遍历保留小者。
func (s *Server) lessTerm(i, j int) bool {
	a, b := s.addrs[i], s.addrs[j]
	if a.everLeased != b.everLeased {
		return !a.everLeased
	}
	if a.everLeased && !a.termAt.Equal(b.termAt) {
		return a.termAt.Before(b.termAt)
	}
	return false
}

// checkClock 检测时钟回拨。
func (s *Server) checkClock(now time.Time) error {
	if s.hasLast && now.Before(s.last) {
		return ErrClockRollback
	}
	return nil
}

// touch 在操作成功后推进已见时刻。
func (s *Server) touch(now time.Time) {
	s.last = now
	s.hasLast = true
}

// Discover 为客户端按固定优先次序选址并暂留：
//  1. 客户端已有有效租约或暂留的地址（租约优先，暂留沿用不刷新起点）；
//  2. 客户端最近一次持有过的地址且空闲；
//  3. 请求地址在池内且空闲；
//  4. 空闲地址中租约终止时刻最早者（从未有过租约者视为最早），并列取地址小者。
//
// reqAddr 为可选的请求地址，传 nil 表示无请求地址。
// 拒绝顺序：时钟回拨、客户端为空、请求地址不在池内、池已耗尽。
func (s *Server) Discover(client string, reqAddr *int, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(now); err != nil {
		return 0, err
	}
	if client == "" {
		return 0, ErrEmptyClient
	}
	if reqAddr != nil && !s.inPool(*reqAddr) {
		return 0, ErrAddrOutOfPool
	}
	s.normalize(now)

	// 规则 1：已有有效租约（优先）或有效暂留（沿用，不刷新起点）。
	for i := range s.addrs {
		if s.addrs[i].leaseClient == client {
			s.touch(now)
			return s.lo + i, nil
		}
	}
	for i := range s.addrs {
		if s.addrs[i].holdClient == client {
			s.touch(now)
			return s.lo + i, nil
		}
	}

	pick := -1
	// 规则 2：最近一次持有过的地址且空闲。
	if addr, ok := s.lastHeld[client]; ok && s.inPool(addr) && s.free(addr-s.lo, now) {
		pick = addr
	}
	// 规则 3：请求地址在池内且空闲。
	if pick < 0 && reqAddr != nil && s.free(*reqAddr-s.lo, now) {
		pick = *reqAddr
	}
	// 规则 4：空闲地址中租约终止时刻最早者，并列取地址小者。
	if pick < 0 {
		best := -1
		for i := range s.addrs {
			if !s.free(i, now) {
				continue
			}
			if best < 0 || s.lessTerm(i, best) {
				best = i
			}
		}
		if best >= 0 {
			pick = s.lo + best
		}
	}
	if pick < 0 {
		return 0, ErrPoolExhausted
	}

	st := &s.addrs[pick-s.lo]
	st.holdClient = client
	st.holdEnd = now.Add(s.hold)
	s.touch(now)
	return pick, nil
}

// Confirm 确认客户端对地址的租约。地址须是该客户端的有效暂留或其有效租约
// （后者即续租）。成功后租约终点为 now+L（续租从现在起算，不叠加剩余），
// 并清除该暂留。
// 拒绝顺序：时钟回拨、地址不在池内、地址被他人持有或暂留、
// 该客户端无有效暂留或租约。
func (s *Server) Confirm(client string, addr int, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(now); err != nil {
		return err
	}
	if !s.inPool(addr) {
		return ErrAddrOutOfPool
	}
	s.normalize(now)

	st := &s.addrs[addr-s.lo]
	if (st.leaseClient != "" && st.leaseClient != client) ||
		(st.holdClient != "" && st.holdClient != client) {
		return ErrAddrBusy
	}
	if client == "" || (st.holdClient != client && st.leaseClient != client) {
		return ErrNoHoldOrLease
	}

	st.leaseClient = client
	st.leaseEnd = now.Add(s.lease)
	st.holdClient = ""
	st.holdEnd = time.Time{}
	st.everLeased = true
	s.lastHeld[client] = addr
	s.touch(now)
	return nil
}

// Release 释放地址，仅限当前租约持有者。租约终止时刻记为释放时刻。
// 拒绝顺序：时钟回拨、地址不在池内、非持有者。
func (s *Server) Release(client string, addr int, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(now); err != nil {
		return err
	}
	if !s.inPool(addr) {
		return ErrAddrOutOfPool
	}
	s.normalize(now)

	st := &s.addrs[addr-s.lo]
	if client == "" || st.leaseClient != client {
		return ErrNotHolder
	}
	st.leaseClient = ""
	st.leaseEnd = time.Time{}
	st.termAt = now
	s.touch(now)
	return nil
}

// Decline 拒收地址，限当前租约持有者或暂留者。清除其租约或暂留，
// 并使地址进入隔离期 [now, now+Q)。持有者拒收时租约终止时刻记为
// 拒收时刻；暂留者拒收不改变租约终止时刻。
// 拒绝顺序：时钟回拨、地址不在池内、非持有者（含暂留者）。
func (s *Server) Decline(client string, addr int, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(now); err != nil {
		return err
	}
	if !s.inPool(addr) {
		return ErrAddrOutOfPool
	}
	s.normalize(now)

	st := &s.addrs[addr-s.lo]
	isLeaseHolder := client != "" && st.leaseClient == client
	isHoldOwner := client != "" && st.holdClient == client
	if !isLeaseHolder && !isHoldOwner {
		return ErrNotHolder
	}
	if isLeaseHolder {
		st.leaseClient = ""
		st.leaseEnd = time.Time{}
		st.termAt = now
	}
	if isHoldOwner {
		st.holdClient = ""
		st.holdEnd = time.Time{}
	}
	st.quarUntil = now.Add(s.quar)
	s.touch(now)
	return nil
}
