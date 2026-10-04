package paywall

import (
	"errors"
	"sync"

	"ontology/identity"
	"ontology/meter"
)

var (
	ErrInvalidArgument = meter.ErrInvalidArgument
	ErrClockSkew       = meter.ErrClockSkew
	ErrArticleNotFound = errors.New("article not found")
	ErrAlreadyBound    = identity.ErrAlreadyBound
	ErrNotBound        = identity.ErrNotBound
	ErrNotSubscribed   = errors.New("not subscribed")
	ErrGiftQuota       = errors.New("gift quota exhausted")
)

type Reason uint8

const (
	Denied Reason = iota
	Subscription
	FreeArticle
	AlreadyUnlocked
	Gift
	Quota
)

type Result struct {
	Allowed bool
	Reason  Reason
	TokenID int64
}

type Token struct {
	id      int64
	article string
	issued  int64
}

func (t *Token) ID() int64 {
	return t.id
}

type Paywall struct {
	mu        sync.Mutex
	n         int
	m         int64
	g         int
	k         int
	e         int64
	ledger    *meter.Ledger
	binder    *identity.Binder
	articles  map[string]bool
	subs      map[string]int64
	tokens    map[int64]*tokenState
	giftCount map[string]map[int64]int
	nextToken int64
}

type tokenState struct {
	token    Token
	redeemed map[string]struct{}
}

func New(n int, m int64, g, k int, e int64) *Paywall {
	if n < 0 || n > 1000 ||
		m < 1 || m > 1_000_000_000 ||
		g < 1 || g > 1_000_000_000 ||
		k < 1 || k > 1_000_000_000 ||
		e < 1 || e > 1_000_000_000 {
		panic(ErrInvalidArgument)
	}
	return &Paywall{
		n:         n,
		m:         m,
		g:         g,
		k:         k,
		e:         e,
		ledger:    meter.New(n, m),
		binder:    identity.New(),
		articles:  map[string]bool{},
		subs:      map[string]int64{},
		tokens:    map[int64]*tokenState{},
		giftCount: map[string]map[int64]int{},
	}
}

func (p *Paywall) AddArticle(id string, free bool) error {
	if id == "" {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.articles[id] = free
	return nil
}

func (p *Paywall) Read(now int64, device, article string, token *Token) (Result, error) {
	if now < 0 || now > 1_000_000_000_000 || device == "" || article == "" || (token != nil && token.id < 1) {
		return Result{}, ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ledger.Check(now); err != nil {
		return Result{}, err
	}
	free, ok := p.articles[article]
	if !ok {
		return Result{}, ErrArticleNotFound
	}
	if err := p.ledger.Advance(now); err != nil {
		return Result{}, err
	}

	subject := p.binder.Subject(device)
	if user, bound := p.binder.BoundUser(device); bound && now < p.subs[user] {
		return Result{Allowed: true, Reason: Subscription}, nil
	}
	if free {
		return Result{Allowed: true, Reason: FreeArticle}, nil
	}
	if _, ok := p.ledger.Lookup(subject, now, article); ok {
		return Result{Allowed: true, Reason: AlreadyUnlocked}, nil
	}
	if state, ok := p.validGift(subject, article, now, token); ok {
		state.redeemed[subject] = struct{}{}
		p.ledger.AddGift(subject, now, article)
		return Result{Allowed: true, Reason: Gift, TokenID: token.id}, nil
	}
	if p.ledger.Used(subject, now) < p.n {
		p.ledger.AddQuota(subject, now, article)
		return Result{Allowed: true, Reason: Quota}, nil
	}
	return Result{Allowed: false, Reason: Denied}, nil
}

func (p *Paywall) Login(now int64, device, user string) error {
	if now < 0 || now > 1_000_000_000_000 || device == "" || user == "" {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.binder.Check(now); err != nil {
		return normalizeIdentityError(err)
	}
	if err := p.ledger.Check(now); err != nil {
		return err
	}
	if _, bound := p.binder.BoundUser(device); bound {
		return ErrAlreadyBound
	}
	if _, err := p.binder.Login(now, device, user); err != nil {
		return normalizeIdentityError(err)
	}
	if err := p.ledger.Advance(now); err != nil {
		return err
	}
	p.ledger.Merge(now, user, device)
	return nil
}

func (p *Paywall) Logout(now int64, device string) error {
	if now < 0 || now > 1_000_000_000_000 || device == "" {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.binder.Check(now); err != nil {
		return normalizeIdentityError(err)
	}
	if err := p.ledger.Check(now); err != nil {
		return err
	}
	if _, bound := p.binder.BoundUser(device); !bound {
		return ErrNotBound
	}
	if err := p.binder.Logout(now, device); err != nil {
		return normalizeIdentityError(err)
	}
	return p.ledger.Advance(now)
}

func (p *Paywall) Subscribe(now int64, user string, until int64) error {
	if now < 0 || now > 1_000_000_000_000 || user == "" || until <= now {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ledger.Check(now); err != nil {
		return err
	}
	if err := p.ledger.Advance(now); err != nil {
		return err
	}
	p.subs[user] = until
	return nil
}

func (p *Paywall) Gift(now int64, user, article string) (*Token, error) {
	if now < 0 || now > 1_000_000_000_000 || user == "" || article == "" {
		return nil, ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ledger.Check(now); err != nil {
		return nil, err
	}
	if _, ok := p.articles[article]; !ok {
		return nil, ErrArticleNotFound
	}
	if now >= p.subs[user] {
		return nil, ErrNotSubscribed
	}

	month := p.ledger.Month(now)
	months, ok := p.giftCount[user]
	if !ok {
		months = map[int64]int{}
		p.giftCount[user] = months
	}
	if months[month] >= p.g {
		return nil, ErrGiftQuota
	}
	if err := p.ledger.Advance(now); err != nil {
		return nil, err
	}

	p.nextToken++
	token := &Token{id: p.nextToken, article: article, issued: now}
	p.tokens[token.id] = &tokenState{token: *token, redeemed: map[string]struct{}{}}
	months[month]++
	return token, nil
}

func (p *Paywall) validGift(subject, article string, now int64, token *Token) (*tokenState, bool) {
	if token == nil {
		return nil, false
	}
	state, ok := p.tokens[token.id]
	if !ok || state.token != *token || state.token.article != article {
		return nil, false
	}
	_, alreadyRedeemed := state.redeemed[subject]
	if !alreadyRedeemed && len(state.redeemed) >= p.k {
		return nil, false
	}
	if now < token.issued || now >= token.issued+p.e {
		return nil, false
	}
	return state, true
}

func normalizeIdentityError(err error) error {
	switch {
	case errors.Is(err, identity.ErrClockSkew):
		return ErrClockSkew
	case errors.Is(err, identity.ErrInvalidArgument):
		return ErrInvalidArgument
	case errors.Is(err, identity.ErrAlreadyBound):
		return ErrAlreadyBound
	case errors.Is(err, identity.ErrNotBound):
		return ErrNotBound
	default:
		return err
	}
}
