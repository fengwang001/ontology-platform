// Package paywall 实现订阅、礼赠与每次阅读的放行判定。
package paywall

import (
	"errors"
	"strconv"
	"sync"

	"ontology/identity"
	"ontology/meter"
)

var (
	// ErrInvalidArgument 表示参数非法（含构造参数、标识为空、until<=now 等）。
	ErrInvalidArgument = errors.New("paywall: invalid argument")
	// ErrClockRollback 表示 now 小于已接受操作的最大 now。
	ErrClockRollback = errors.New("paywall: clock moved backwards")
	// ErrArticleUnknown 表示文章未登记。
	ErrArticleUnknown = errors.New("paywall: article not registered")
	// ErrAlreadyBound 同 identity.ErrAlreadyBound（errors.Is 互通）。
	ErrAlreadyBound = identity.ErrAlreadyBound
	// ErrNotBound 同 identity.ErrNotBound（errors.Is 互通）。
	ErrNotBound = identity.ErrNotBound
	// ErrNotSubscribed 表示礼赠签发时用户订阅无效。
	ErrNotSubscribed = errors.New("paywall: user is not an active subscriber")
	// ErrGiftExhausted 表示该用户本月礼赠签发额度已用尽。
	ErrGiftExhausted = errors.New("paywall: monthly gift quota exhausted")
)

// Reason 是放行原因。
type Reason int

const (
	// ReasonSubscription 订阅用户在订阅期内放行。
	ReasonSubscription Reason = iota
	// ReasonFree 免费文章放行。
	ReasonFree
	// ReasonUnlocked 本月账中已有该文章。
	ReasonUnlocked
	// ReasonGift 有效礼赠令牌放行。
	ReasonGift
	// ReasonQuota 消耗每月免费额度放行。
	ReasonQuota
)

// ReasonBlocked 表示 Read 被拦截（Allowed 为 false）。
const ReasonBlocked Reason = -1

// Result 是一次 Read 的结果。
type Result struct {
	Allowed bool
	Reason  Reason
}

type giftToken struct {
	article   string
	issuedAt  int64
	redeemers map[string]struct{}
}

type monthlyCount struct {
	month int64
	count int64
}

// Paywall 是付费墙门面。
type Paywall struct {
	mu    sync.Mutex
	n, g  int64
	k     int64
	e     int64
	clock int64 // 已接受操作的最大 now；-1 表示尚无接受的操作

	articles map[string]bool // 文章 -> 是否免费
	subs     map[string]int64
	issued   map[string]monthlyCount // 用户 -> 本月礼赠签发数
	tokens   map[int64]giftToken
	nextTok  int64

	mt *meter.Meter
	id *identity.Service
}

// Config 为构造参数。
type Config struct {
	N int64 // 每月免费篇数
	M int64 // 月长（秒）
	G int64 // 每月礼赠额度
	K int64 // 每个礼赠令牌可领取主体数
	E int64 // 令牌有效期（秒）
}

// New 创建付费墙。
func New(cfg Config) (*Paywall, error) {
	if cfg.N < 0 || cfg.N > 1000 ||
		cfg.M < 1 || cfg.M > 1_000_000_000 ||
		cfg.G < 1 || cfg.G > 1_000_000_000 ||
		cfg.K < 1 || cfg.K > 1_000_000_000 ||
		cfg.E < 1 || cfg.E > 1_000_000_000 {
		return nil, ErrInvalidArgument
	}
	mt, err := meter.New(cfg.N, cfg.M)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	return &Paywall{
		n: cfg.N, g: cfg.G, k: cfg.K, e: cfg.E, clock: -1,
		articles: make(map[string]bool),
		subs:     make(map[string]int64),
		issued:   make(map[string]monthlyCount),
		tokens:   make(map[int64]giftToken),
		mt:       mt,
		id:       identity.New(mt),
	}, nil
}

// AddArticle 登记文章；free 为 true 时为免费文章。
func (p *Paywall) AddArticle(id []byte, free bool) error {
	if len(id) == 0 {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	k := string(id)
	if existing, ok := p.articles[k]; ok && existing != free {
		return ErrInvalidArgument
	}
	p.articles[k] = free
	return nil
}

func validNow(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

func (p *Paywall) checkClock(now int64) error {
	if now < p.clock {
		return ErrClockRollback
	}
	return nil
}

// Subscribe 设置用户订阅到 until（要求 until > now），重复调用覆盖。
func (p *Paywall) Subscribe(now int64, user []byte, until int64) error {
	if len(user) == 0 || !validNow(now) || until <= now {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkClock(now); err != nil {
		return err
	}
	p.clock = now
	p.subs[string(user)] = until
	return nil
}

// Gift 为已订阅用户就指定文章签发新令牌，返回自 1 递增的令牌编号。
func (p *Paywall) Gift(now int64, user, article []byte) (int64, error) {
	if len(user) == 0 || len(article) == 0 || !validNow(now) {
		return 0, ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkClock(now); err != nil {
		return 0, err
	}
	if _, ok := p.articles[string(article)]; !ok {
		return 0, ErrArticleUnknown
	}
	until, ok := p.subs[string(user)]
	if !ok || now >= until {
		return 0, ErrNotSubscribed
	}
	month := p.mt.Month(now)
	mc := p.issued[string(user)]
	if mc.month != month {
		mc = monthlyCount{month: month}
	}
	if mc.count >= p.g {
		return 0, ErrGiftExhausted
	}
	p.clock = now
	mc.count++
	p.issued[string(user)] = mc
	p.nextTok++
	p.tokens[p.nextTok] = giftToken{
		article:   string(article),
		issuedAt:  now,
		redeemers: map[string]struct{}{},
	}
	return p.nextTok, nil
}

// Login 绑定设备到用户并合并账目。
func (p *Paywall) Login(now int64, device, user []byte) error {
	if len(device) == 0 || len(user) == 0 || !validNow(now) {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkClock(now); err != nil {
		return err
	}
	if err := p.id.Login(now, device, user); err != nil {
		return err
	}
	p.clock = now
	return nil
}

// Logout 解除设备绑定。
func (p *Paywall) Logout(now int64, device []byte) error {
	if len(device) == 0 || !validNow(now) {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkClock(now); err != nil {
		return err
	}
	if !p.id.IsBound(device) {
		return ErrNotBound
	}
	if err := p.id.Logout(device); err != nil {
		return err
	}
	p.clock = now
	return nil
}

// Read 判定一次阅读；拦截不是错误（err 为 nil，Result.Allowed 为 false）。
// token 为 nil 表示未带令牌，否则为令牌编号的十进制字节串。
func (p *Paywall) Read(now int64, device, article, token []byte) (Result, error) {
	if len(device) == 0 || len(article) == 0 || !validNow(now) {
		return Result{}, ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkClock(now); err != nil {
		return Result{}, err
	}
	free, ok := p.articles[string(article)]
	if !ok {
		return Result{}, ErrArticleUnknown
	}
	// 拦截也是被接受的操作：无论后续是否放行，时钟都推进。
	p.clock = now

	subject, bound := p.id.Principal(device)

	// 1) 订阅：主体必须是用户且订阅有效。
	if bound {
		if until, ok := p.subs[string(subject)]; ok && now < until {
			return Result{Allowed: true, Reason: ReasonSubscription}, nil
		}
	}
	// 2) 免费文章。
	if free {
		return Result{Allowed: true, Reason: ReasonFree}, nil
	}
	// 3) 本月已解锁。
	if _, ok := p.mt.Lookup(subject, article, now); ok {
		return Result{Allowed: true, Reason: ReasonUnlocked}, nil
	}
	// 4) 礼赠：解析并校验令牌。
	if tok, valid := p.validToken(token, article, now, subject); valid {
		tok.redeemers[string(subject)] = struct{}{}
		p.mt.RedeemGift(subject, article, now)
		return Result{Allowed: true, Reason: ReasonGift}, nil
	}
	// 5) 额度。
	if int64(p.mt.Used(subject, now)) < p.n {
		p.mt.RedeemQuota(subject, article, now)
		return Result{Allowed: true, Reason: ReasonQuota}, nil
	}
	return Result{Allowed: false, Reason: ReasonBlocked}, nil
}

// validToken 校验令牌：存在、文章匹配、now 落在半开有效期内，且
// 已领取的不同主体数 < k，或该主体此前已领取过。任何无效情形视同未带。
func (p *Paywall) validToken(raw []byte, article []byte, now int64, subject []byte) (giftToken, bool) {
	if len(raw) == 0 {
		return giftToken{}, false
	}
	id, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || id <= 0 {
		return giftToken{}, false
	}
	tok, ok := p.tokens[id]
	if !ok || tok.article != string(article) {
		return giftToken{}, false
	}
	if now < tok.issuedAt || now >= tok.issuedAt+p.e {
		return giftToken{}, false
	}
	if _, already := tok.redeemers[string(subject)]; already {
		return tok, true
	}
	if int64(len(tok.redeemers)) >= p.k {
		return giftToken{}, false
	}
	return tok, true
}
