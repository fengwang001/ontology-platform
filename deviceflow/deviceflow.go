// Package deviceflow 实现 RFC 8628 风格的设备授权码流程服务。
//
// 服务支持：客户端发起设备授权（Start）、用户凭短码批准或拒绝
// （Authorize）、设备按间隔轮询令牌（Poll），并通过提前轮询惩罚、
// 客户端级间隔回升与活跃授权名额控制节奏。所有操作可并发调用，
// 结果等价于某个串行顺序；相同操作序列（含 gen 返回序列）重放
// 得到完全相同的结果。
package deviceflow

import (
	"container/heap"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
)

// maxNow 是 now 参数的合法上界（含）。
const maxNowValue = int64(1_000_000_000_000_000)

// maxGenAttempts 是 Start 内生成用户码的最大连续失败次数。
const maxGenAttempts = 100

// Status 是授权记录的状态。
type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusDenied   Status = "denied"
	StatusConsumed Status = "consumed"
)

// Kind 区分被拒绝操作的类别。
type Kind int

const (
	KindInvalidParam      Kind = iota // 参数非法
	KindClockRollback                 // 时钟回退
	KindUnknownDeviceCode             // 未知设备码
	KindRateLimited                   // 限流
	KindTooManyActive                 // 超限（活跃授权名额已满）
	KindCodeGeneration                // 生成失败
	KindNotFound                      // 未找到
	KindExpired                       // 已过期
	KindAlreadyDecided                // 已决定
)

func (k Kind) String() string {
	switch k {
	case KindInvalidParam:
		return "invalid_param"
	case KindClockRollback:
		return "clock_rollback"
	case KindUnknownDeviceCode:
		return "unknown_device_code"
	case KindRateLimited:
		return "rate_limited"
	case KindTooManyActive:
		return "too_many_active"
	case KindCodeGeneration:
		return "code_generation_failed"
	case KindNotFound:
		return "not_found"
	case KindExpired:
		return "expired"
	case KindAlreadyDecided:
		return "already_decided"
	}
	return "unknown"
}

// Error 是被拒绝操作返回的错误，携带类别与定位信息。
type Error struct {
	Kind Kind
	Msg  string
	// S 与 U 仅在 Kind 为 KindRateLimited 时有效：
	// S 为当前窗口内惩罚次数，U 为最早可发起时刻。
	S int
	U int64
}

func (e *Error) Error() string {
	if e.Kind == KindRateLimited {
		return fmt.Sprintf("%s: %s (s=%d u=%d)", e.Kind, e.Msg, e.S, e.U)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Msg)
}

// AsKind 从 err 链中取出 *Error 的类别；ok 为 false 表示不是 *Error。
func AsKind(err error) (Kind, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind, true
	}
	return 0, false
}

// Outcome 是 Poll 被接受后的返回类别。
type Outcome int

const (
	OutcomeInvalidGrant         Outcome = iota // 无效授权（已 consumed）
	OutcomeExpired                             // 已过期
	OutcomeSlowDown                            // 过快
	OutcomeAuthorizationPending                // 等待授权
	OutcomeToken                               // 令牌
	OutcomeAccessDenied                        // 拒绝访问
)

func (o Outcome) String() string {
	switch o {
	case OutcomeInvalidGrant:
		return "invalid_grant"
	case OutcomeExpired:
		return "expired"
	case OutcomeSlowDown:
		return "slow_down"
	case OutcomeAuthorizationPending:
		return "authorization_pending"
	case OutcomeToken:
		return "token"
	case OutcomeAccessDenied:
		return "access_denied"
	}
	return "unknown"
}

// Config 是服务构造参数。E、I0、D、Imax、H 均为秒。
type Config struct {
	E    int64 // 设备码有效期，1..1e9
	I0   int64 // 初始间隔，1..1e6 且 I0 <= Imax
	D    int64 // 间隔步进，1..1e6
	Imax int64 // 间隔上限，1..1e9
	H    int64 // 惩罚窗口，1..1e9
	Cmax int   // 每客户端活跃授权上限，1..1e6
	Z    int   // 惩罚锁定阈值，1..1e6
	// Gen 是可注入的用户码生成器；为 nil 时使用默认生成器。
	Gen func() string
}

func (c Config) validate() error {
	bad := func(name string) error {
		return &Error{Kind: KindInvalidParam, Msg: "invalid config field: " + name}
	}
	if c.E < 1 || c.E > 1_000_000_000 {
		return bad("E")
	}
	if c.I0 < 1 || c.I0 > 1_000_000 {
		return bad("I0")
	}
	if c.D < 1 || c.D > 1_000_000 {
		return bad("D")
	}
	if c.Imax < 1 || c.Imax > 1_000_000_000 {
		return bad("Imax")
	}
	if c.I0 > c.Imax {
		return bad("I0>Imax")
	}
	if c.H < 1 || c.H > 1_000_000_000 {
		return bad("H")
	}
	if c.Cmax < 1 || c.Cmax > 1_000_000 {
		return bad("Cmax")
	}
	if c.Z < 1 || c.Z > 1_000_000 {
		return bad("Z")
	}
	return nil
}

// StartResult 是 Start 成功时的返回。
type StartResult struct {
	DeviceCode string // d1、d2…，仅在成功发起时按序递增
	UserCode   string // gen 返回的原串（未规范化）
	Interval   int64  // 发起时刻的客户端基础间隔
	ExpiresAt  int64  // now+E
}

// PollResult 是 Poll 被接受后的返回。
type PollResult struct {
	Outcome     Outcome
	Token       string // 仅 OutcomeToken 时非空
	TokenSeq    int    // 令牌序号，从 1 递增；非 OutcomeToken 时为 0
	Interval    int64  // 操作后的授权间隔
	NextAllowed int64  // 操作后的下次允许轮询时刻
}

// IntervalInfo 是 Interval 的返回。
type IntervalInfo struct {
	Status      Status
	Interval    int64
	NextAllowed int64
}

// ClientIntervalInfo 是 ClientInterval 的返回。
type ClientIntervalInfo struct {
	Interval int64 // 客户端基础间隔 min(Imax, I0+D*s)
	S        int   // 窗口内惩罚次数
	Limited  bool  // 是否被限流（S >= Z）
	U        int64 // 最早可发起时刻，仅 Limited 时有效
}

// auth 是一条授权记录。
type auth struct {
	deviceCode  string
	client      string
	userCode    string // 规范化后的用户码
	status      Status
	expiresAt   int64
	interval    int64
	nextAllowed int64
	seq         int64 // 创建序号，用于堆内排序
}

// codeList 记录同一规范化用户码的授权（按创建序，expiresAt 非递减），
// head 之前的元素已被到期回收。
type codeList struct {
	auths []*auth
	head  int
}

// clientState 是每客户端的增量统计状态。
type clientState struct {
	active     int     // 状态为 pending/approved 且 expiresAt > maxNow 的授权数
	events     []int64 // 过快事件发生时刻，非递减
	eventHead  int     // events[:eventHead] 已出窗（t+H <= maxNow）
	expireQ    []*auth // 该客户端未被回收的授权，按创建序（expiresAt 非递减）
	expireHead int     // expireQ[:expireHead] 已被堆回收
}

// Service 是设备授权码流程服务。所有方法可并发调用。
type Service struct {
	cfg Config

	mu      sync.Mutex
	maxNow  int64 // 已被接受操作见过的最大 now
	devSeq  int64 // 设备码序号（仅成功 Start 递增）
	tokSeq  int   // 令牌序号（从 1 递增）
	authSeq int64 // 授权创建序号（堆排序用）

	byDevice map[string]*auth
	latest   map[string]*auth     // 规范化用户码 -> 最近创建的授权
	codes    map[string]*codeList // 规范化用户码 -> 未到期授权列表
	clients  map[string]*clientState
	expiry   expiryHeap

	lastGenCalls int // 最近一次 Start 内的 gen 调用次数
	lastReapPops int // 最近一次到期回收的堆弹出次数
}

// New 校验配置并构造服务；配置非法时整体拒绝。
func New(cfg Config) (*Service, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.Gen == nil {
		cfg.Gen = defaultGen
	}
	return &Service{
		cfg:      cfg,
		byDevice: make(map[string]*auth),
		latest:   make(map[string]*auth),
		codes:    make(map[string]*codeList),
		clients:  make(map[string]*clientState),
	}, nil
}

// Start 为客户端发起一次设备授权。
//
// 拒绝次序：参数非法、时钟回退、限流、超限、生成失败。
func (s *Service) Start(client string, now int64) (*StartResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if client == "" {
		return nil, &Error{Kind: KindInvalidParam, Msg: "empty client"}
	}
	if now < 0 || now > maxNowValue {
		return nil, &Error{Kind: KindInvalidParam, Msg: "now out of range"}
	}
	if now < s.maxNow {
		return nil, &Error{Kind: KindClockRollback, Msg: fmt.Sprintf("now=%d < maxNow=%d", now, s.maxNow)}
	}

	cs := s.clientStateLocked(client)
	window := windowEvents(cs, now, s.cfg.H)
	pen := len(window)
	if pen >= s.cfg.Z {
		u := window[pen-s.cfg.Z] + s.cfg.H
		return nil, &Error{Kind: KindRateLimited, Msg: "client is locked by slow-down penalties", S: pen, U: u}
	}
	if s.activeCountLocked(cs, now) >= s.cfg.Cmax {
		return nil, &Error{Kind: KindTooManyActive, Msg: "active authorization limit reached"}
	}

	var raw, norm string
	calls := 0
	for {
		raw = s.cfg.Gen()
		calls++
		n, ok := normalizeCode(raw)
		if ok && !s.duplicateLocked(n, now) {
			norm = n
			break
		}
		if calls == maxGenAttempts {
			s.lastGenCalls = calls
			return nil, &Error{Kind: KindCodeGeneration, Msg: "user code generation failed 100 times"}
		}
	}
	s.lastGenCalls = calls

	s.advanceClockLocked(now)
	s.reapClientEventsLocked(cs, now)

	s.devSeq++
	s.authSeq++
	a := &auth{
		deviceCode:  "d" + strconv.FormatInt(s.devSeq, 10),
		client:      client,
		userCode:    norm,
		status:      StatusPending,
		expiresAt:   now + s.cfg.E,
		interval:    s.baseInterval(pen),
		nextAllowed: now,
		seq:         s.authSeq,
	}
	s.byDevice[a.deviceCode] = a
	s.latest[norm] = a
	cl := s.codes[norm]
	if cl == nil {
		cl = &codeList{}
		s.codes[norm] = cl
	}
	cl.auths = append(cl.auths, a)
	cs.active++
	cs.expireQ = append(cs.expireQ, a)
	heap.Push(&s.expiry, expiryEntry{expiresAt: a.expiresAt, seq: a.seq, auth: a})

	return &StartResult{
		DeviceCode: a.deviceCode,
		UserCode:   raw,
		Interval:   a.interval,
		ExpiresAt:  a.expiresAt,
	}, nil
}

// Authorize 由用户凭用户码批准（approve=true）或拒绝授权。
//
// 拒绝次序：参数非法、时钟回退、未找到、已过期、已决定。
func (s *Service) Authorize(userCode string, approve bool, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	norm, ok := normalizeCode(userCode)
	if !ok {
		return &Error{Kind: KindInvalidParam, Msg: "user code not compliant"}
	}
	if now < 0 || now > maxNowValue {
		return &Error{Kind: KindInvalidParam, Msg: "now out of range"}
	}
	if now < s.maxNow {
		return &Error{Kind: KindClockRollback, Msg: fmt.Sprintf("now=%d < maxNow=%d", now, s.maxNow)}
	}
	a := s.latest[norm]
	if a == nil {
		return &Error{Kind: KindNotFound, Msg: "no authorization for user code"}
	}
	if now >= a.expiresAt {
		return &Error{Kind: KindExpired, Msg: "authorization expired"}
	}
	if a.status != StatusPending {
		return &Error{Kind: KindAlreadyDecided, Msg: "authorization already " + string(a.status)}
	}

	s.advanceClockLocked(now)
	cs := s.clients[a.client]
	s.reapClientEventsLocked(cs, now)
	if approve {
		a.status = StatusApproved
	} else {
		a.status = StatusDenied
		cs.active--
	}
	return nil
}

// Poll 由设备凭设备码轮询。
//
// 参数非法、时钟回退、未知设备码为拒绝；其余均为被接受的操作，
// 按 consumed、已过期、过快、状态判定 的固定次序返回结果。
func (s *Service) Poll(deviceCode string, now int64) (*PollResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if deviceCode == "" {
		return nil, &Error{Kind: KindInvalidParam, Msg: "empty device code"}
	}
	if now < 0 || now > maxNowValue {
		return nil, &Error{Kind: KindInvalidParam, Msg: "now out of range"}
	}
	if now < s.maxNow {
		return nil, &Error{Kind: KindClockRollback, Msg: fmt.Sprintf("now=%d < maxNow=%d", now, s.maxNow)}
	}
	a := s.byDevice[deviceCode]
	if a == nil {
		return nil, &Error{Kind: KindUnknownDeviceCode, Msg: "device code never issued"}
	}

	s.advanceClockLocked(now)
	cs := s.clients[a.client]
	s.reapClientEventsLocked(cs, now)

	res := &PollResult{}
	switch {
	case a.status == StatusConsumed:
		res.Outcome = OutcomeInvalidGrant
	case now >= a.expiresAt:
		res.Outcome = OutcomeExpired
	case now < a.nextAllowed:
		a.interval += s.cfg.D
		if a.interval > s.cfg.Imax {
			a.interval = s.cfg.Imax
		}
		a.nextAllowed = now + a.interval
		cs.events = append(cs.events, now)
		res.Outcome = OutcomeSlowDown
	default:
		a.nextAllowed = now + a.interval
		switch a.status {
		case StatusPending:
			res.Outcome = OutcomeAuthorizationPending
		case StatusApproved:
			s.tokSeq++
			res.Outcome = OutcomeToken
			res.Token = "tok-" + strconv.Itoa(s.tokSeq)
			res.TokenSeq = s.tokSeq
			a.status = StatusConsumed
			cs.active--
		case StatusDenied:
			res.Outcome = OutcomeAccessDenied
			a.status = StatusConsumed
		}
	}
	res.Interval = a.interval
	res.NextAllowed = a.nextAllowed
	return res, nil
}

// Interval 返回授权的状态、间隔与下次允许轮询时刻（只读）。
func (s *Service) Interval(deviceCode string) (*IntervalInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if deviceCode == "" {
		return nil, &Error{Kind: KindInvalidParam, Msg: "empty device code"}
	}
	a := s.byDevice[deviceCode]
	if a == nil {
		return nil, &Error{Kind: KindUnknownDeviceCode, Msg: "device code never issued"}
	}
	return &IntervalInfo{Status: a.status, Interval: a.interval, NextAllowed: a.nextAllowed}, nil
}

// ClientInterval 返回客户端基础间隔、惩罚次数、是否限流与 u（只读，
// 不推进时钟）。
func (s *Service) ClientInterval(client string, now int64) (*ClientIntervalInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if client == "" {
		return nil, &Error{Kind: KindInvalidParam, Msg: "empty client"}
	}
	if now < 0 || now > maxNowValue {
		return nil, &Error{Kind: KindInvalidParam, Msg: "now out of range"}
	}
	if now < s.maxNow {
		return nil, &Error{Kind: KindClockRollback, Msg: fmt.Sprintf("now=%d < maxNow=%d", now, s.maxNow)}
	}

	var window []int64
	if cs := s.clients[client]; cs != nil {
		window = windowEvents(cs, now, s.cfg.H)
	}
	pen := len(window)
	info := &ClientIntervalInfo{
		Interval: s.baseInterval(pen),
		S:        pen,
		Limited:  pen >= s.cfg.Z,
	}
	if info.Limited {
		info.U = window[pen-s.cfg.Z] + s.cfg.H
	}
	return info, nil
}

// clientStateLocked 返回客户端的增量统计状态，不存在时创建。
func (s *Service) clientStateLocked(client string) *clientState {
	cs := s.clients[client]
	if cs == nil {
		cs = &clientState{}
		s.clients[client] = cs
	}
	return cs
}

// windowEvents 返回窗口内（t+H > now）的过快事件，按发生时刻升序。
// 只读，不推进 eventHead。
func windowEvents(cs *clientState, now, H int64) []int64 {
	ev := cs.events[cs.eventHead:]
	idx := sort.Search(len(ev), func(i int) bool { return ev[i]+H > now })
	return ev[idx:]
}

// baseInterval 计算客户端基础间隔 min(Imax, I0+D*pen)。
func (s *Service) baseInterval(pen int) int64 {
	if int64(pen) > (s.cfg.Imax-s.cfg.I0)/s.cfg.D {
		return s.cfg.Imax
	}
	return s.cfg.I0 + s.cfg.D*int64(pen)
}

// activeCountLocked 计算 now 时刻的活跃授权数：cs.active 维护在
// maxNow 时刻，此处减去 (maxNow, now] 内到期的活跃授权（只读扫描）。
func (s *Service) activeCountLocked(cs *clientState, now int64) int {
	n := cs.active
	for _, a := range cs.expireQ[cs.expireHead:] {
		if a.expiresAt > now {
			break
		}
		if a.status == StatusPending || a.status == StatusApproved {
			n--
		}
	}
	return n
}

// duplicateLocked 判断规范化用户码是否与任一尚未到期（不论状态）
// 的授权重复。
func (s *Service) duplicateLocked(norm string, now int64) bool {
	cl := s.codes[norm]
	if cl == nil {
		return false
	}
	live := cl.auths[cl.head:]
	return len(live) > 0 && live[len(live)-1].expiresAt > now
}

// advanceClockLocked 在接受操作时推进全局时钟并回收到期授权。
func (s *Service) advanceClockLocked(now int64) {
	if now > s.maxNow {
		s.maxNow = now
	}
	s.reapLocked(now)
}

// reapLocked 弹出所有 expiresAt <= now 的授权，维护活跃名额与
// 用户码未到期索引。弹出次数等于本次到期的授权数。
func (s *Service) reapLocked(now int64) {
	pops := 0
	for len(s.expiry) > 0 && s.expiry[0].expiresAt <= now {
		e := heap.Pop(&s.expiry).(expiryEntry)
		pops++
		a := e.auth
		cs := s.clients[a.client]
		if a.status == StatusPending || a.status == StatusApproved {
			cs.active--
		}
		cl := s.codes[a.userCode]
		cl.head++
		if cl.head == len(cl.auths) {
			delete(s.codes, a.userCode)
		}
		cs.expireHead++
		if cs.expireHead > 1024 && cs.expireHead*2 >= len(cs.expireQ) {
			cs.expireQ = append([]*auth(nil), cs.expireQ[cs.expireHead:]...)
			cs.expireHead = 0
		}
	}
	s.lastReapPops = pops
}

// reapClientEventsLocked 推进客户端的过快事件窗口（t+H <= now 出窗）。
// 仅在已接受操作上调用，now 已成为新的 maxNow，不会误删未来仍
// 在窗口内的事件。
func (s *Service) reapClientEventsLocked(cs *clientState, now int64) {
	h := cs.eventHead
	for h < len(cs.events) && cs.events[h]+s.cfg.H <= now {
		h++
	}
	cs.eventHead = h
	if cs.eventHead > 1024 && cs.eventHead*2 >= len(cs.events) {
		cs.events = append([]int64(nil), cs.events[cs.eventHead:]...)
		cs.eventHead = 0
	}
}
