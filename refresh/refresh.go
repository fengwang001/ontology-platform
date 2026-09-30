// Package refresh 实现刷新令牌轮换（refresh token rotation）与重用检测器。
//
// 每次续期都会换发新令牌并作废旧令牌；旧令牌在宽限窗口内被再次出示视为
// 客户端重试（原样返回后继），窗口外或后继已失效则视为盗用并立即吊销整个
// 登录家族，从而保证每个家族任意时刻至多一个可用令牌。
package refresh

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// 配置校验错误，可通过 errors.Is 区分。
var (
	// ErrNonPositiveL：令牌寿命 L 非正。
	ErrNonPositiveL = errors.New("refresh: token lifetime L must be positive")
	// ErrNonPositiveD：家族绝对寿命 D 非正。
	ErrNonPositiveD = errors.New("refresh: family absolute lifetime D must be positive")
	// ErrNegativeG：宽限 G 为负。
	ErrNegativeG = errors.New("refresh: reuse grace G must not be negative")
	// ErrGraceTooLarge：宽限 G 不小于 L。
	ErrGraceTooLarge = errors.New("refresh: reuse grace G must be shorter than token lifetime L")
	// ErrAbsoluteShorter：绝对寿命 D 小于 L。
	ErrAbsoluteShorter = errors.New("refresh: family absolute lifetime D must be at least token lifetime L")
)

// 出示令牌时的拒绝原因，按固定优先级排列，一次只报第一个。
var (
	// ErrTokenUnknown：令牌标识未知。
	ErrTokenUnknown = errors.New("refresh: unknown token")
	// ErrFamilyRevoked：令牌所在家族已吊销。
	ErrFamilyRevoked = errors.New("refresh: token family revoked")
	// ErrFamilyAbsoluteExpired：家族已达绝对寿命 D。
	ErrFamilyAbsoluteExpired = errors.New("refresh: token family reached absolute lifetime")
	// ErrTokenExpired：家族当前令牌已到期。
	ErrTokenExpired = errors.New("refresh: current token expired")
	// ErrReuseDetected：检测到盗用，家族已被整体吊销。
	ErrReuseDetected = errors.New("refresh: token reuse detected, family revoked")
)

// Config 为检测器的寿命配置。
type Config struct {
	L time.Duration // 单个令牌寿命，必须为正
	D time.Duration // 家族绝对寿命，必须满足 D >= L
	G time.Duration // 已换出令牌的重试宽限，必须满足 0 <= G < L
	// Log 用于打印每次调用的输入、输出与判定依据；nil 时丢弃日志。
	Log io.Writer
}

// Detector 是刷新令牌轮换与重用检测器。
// 所有方法均可被并发调用，内部以单一互斥锁串行化状态变更。
type Detector struct {
	mu sync.Mutex

	cfg         Config
	seq         int // 全局签发序号
	familySeq   int // 家族序号
	families    map[string]*family
	tokenFamily map[string]string // 令牌标识 -> 家族标识
}

// New 校验配置并创建检测器。
//
// 校验按「L 非正、D 非正、G 为负、G 不小于 L、D 小于 L」的顺序进行，
// 配置不合法时返回上述顺序中第一个可区分的错误，检测器不会被创建。
func New(cfg Config) (*Detector, error) {
	if cfg.L <= 0 {
		return nil, ErrNonPositiveL
	}
	if cfg.D <= 0 {
		return nil, ErrNonPositiveD
	}
	if cfg.G < 0 {
		return nil, ErrNegativeG
	}
	if cfg.G >= cfg.L {
		return nil, ErrGraceTooLarge
	}
	if cfg.D < cfg.L {
		return nil, ErrAbsoluteShorter
	}
	d := &Detector{
		cfg:         cfg,
		families:    make(map[string]*family),
		tokenFamily: make(map[string]string),
	}
	d.logf("config accepted: L=%s D=%s G=%s", cfg.L, cfg.D, cfg.G)
	return d, nil
}

// Login 创建登录家族并签发首个令牌。
// 家族标识为 f1、f2……，令牌标识按检测器全局签发顺序生成（t1、t2……）。
// 首令牌到期时刻取 at+L 与 家族创建时刻+D 中较小者。
func (d *Detector) Login(at time.Time) (*Token, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.familySeq++
	famID := fmt.Sprintf("f%d", d.familySeq)
	fam := &family{
		id:        famID,
		createdAt: at,
		tokens:    make(map[string]*token),
	}
	tok := d.issueLocked(fam, at)
	fam.current = tok.id
	d.families[famID] = fam
	d.logf("input=Login at=%s -> output: family=%s token=%s expires=%s reason=login",
		at, famID, tok.id, tok.expires)
	return d.publicToken(tok), nil
}

// Renew 出示令牌进行续期。
//
// 判定按固定顺序只返回第一个原因：令牌未知、家族已吊销、家族已达绝对寿命、
// 当前令牌已到期、盗用。除盗用会立即吊销整个家族外，任何被拒绝的调用
// 都不改变状态。
//
// 出示家族当前令牌：旧令牌变为已换出（记录换出时刻与后继），换发新令牌。
// 出示已换出令牌：距其换出时刻不足宽限 G（严格小于）且其后继仍是家族当前
// 令牌时视为客户端重试，原样返回后继、不签发新令牌、家族不变；否则视为
// 盗用，立即吊销整个家族（含最新令牌）。已换出的令牌不检查自身到期，
// 到期检查只针对家族当前令牌。
func (d *Detector) Renew(tokenID string, at time.Time) (*Token, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	fam := d.familyOfLocked(tokenID)
	if fam == nil {
		d.logf("input=Renew token=%s at=%s -> output: error=%q reason=token unknown",
			tokenID, at, ErrTokenUnknown)
		return nil, ErrTokenUnknown
	}
	if fam.revoked {
		d.logf("input=Renew token=%s at=%s family=%s -> output: error=%q reason=family revoked",
			tokenID, at, fam.id, ErrFamilyRevoked)
		return nil, ErrFamilyRevoked
	}
	absoluteDeadline := fam.createdAt.Add(d.cfg.D)
	if !at.Before(absoluteDeadline) {
		d.logf("input=Renew token=%s at=%s family=%s -> output: error=%q reason=family absolute lifetime reached at=%s",
			tokenID, at, fam.id, ErrFamilyAbsoluteExpired, absoluteDeadline)
		return nil, ErrFamilyAbsoluteExpired
	}

	tok := fam.tokens[tokenID]
	if tok.id == fam.current {
		if !at.Before(tok.expires) {
			d.logf("input=Renew token=%s at=%s family=%s -> output: error=%q reason=current token expired at=%s",
				tokenID, at, fam.id, ErrTokenExpired, tok.expires)
			return nil, ErrTokenExpired
		}
		tok.rotatedAt = at
		next := d.issueLocked(fam, at)
		tok.successor = next.id
		fam.current = next.id
		d.logf("input=Renew token=%s at=%s family=%s -> output: rotated old=%s new=%s newExpires=%s reason=current token renewed",
			tokenID, at, fam.id, tok.id, next.id, next.expires)
		return d.publicToken(next), nil
	}

	// 出示的是已换出令牌：家族当前令牌若已到期，按固定优先级先报到期
	// （已换出令牌自身的到期时间不检查）。
	cur := fam.tokens[fam.current]
	if cur != nil && !at.Before(cur.expires) {
		d.logf("input=Renew token=%s at=%s family=%s -> output: error=%q reason=current token %s expired at=%s",
			tokenID, at, fam.id, ErrTokenExpired, cur.id, cur.expires)
		return nil, ErrTokenExpired
	}

	successor := fam.tokens[tok.successor]
	withinGrace := at.Sub(tok.rotatedAt) < d.cfg.G
	if withinGrace && tok.successor == fam.current && successor != nil {
		d.logf("input=Renew token=%s at=%s family=%s -> output: retry-successor=%s expires=%s reason=rotated token reused within grace (rotatedAt=%s, G=%s, successor still current)",
			tokenID, at, fam.id, successor.id, successor.expires, tok.rotatedAt, d.cfg.G)
		return d.publicToken(successor), nil
	}

	fam.revoked = true
	fam.current = ""
	d.logf("input=Renew token=%s at=%s family=%s -> output: error=%q reason=reuse of rotated token outside retry window (rotatedAt=%s, elapsed=%s, G=%s, successorStillCurrent=%t); family revoked",
		tokenID, at, fam.id, ErrReuseDetected, tok.rotatedAt, at.Sub(tok.rotatedAt), d.cfg.G,
		tok.successor == fam.current)
	return nil, ErrReuseDetected
}

// Logout 按令牌所在家族执行主动登出（吊销整个家族）。
// 对已吊销家族重复登出视为成功且不改变任何状态；未知令牌返回 ErrTokenUnknown。
func (d *Detector) Logout(tokenID string, at time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	fam := d.familyOfLocked(tokenID)
	if fam == nil {
		d.logf("input=Logout token=%s at=%s -> output: error=%q reason=token unknown",
			tokenID, at, ErrTokenUnknown)
		return ErrTokenUnknown
	}
	if fam.revoked {
		d.logf("input=Logout token=%s at=%s family=%s -> output: ok reason=family already revoked, no change",
			tokenID, at, fam.id)
		return nil
	}
	fam.revoked = true
	fam.current = ""
	d.logf("input=Logout token=%s at=%s family=%s -> output: ok reason=active logout, family revoked",
		tokenID, at, fam.id)
	return nil
}

// Token 为对外可见的令牌视图。
type Token struct {
	ID       string
	FamilyID string
	Expires  time.Time
}

// token 是检测器内部保存的令牌状态。
type token struct {
	id        string
	expires   time.Time
	rotatedAt time.Time // 零值表示仍是家族当前令牌
	successor string    // 换出后继标识；空串表示尚未换出
}

// family 是一次登录产生的令牌家族。
type family struct {
	id        string
	createdAt time.Time
	current   string // 当前令牌标识；家族吊销后为空串
	revoked   bool
	tokens    map[string]*token
}

// familyOfLocked 返回令牌所属家族；调用方须持有 d.mu。
func (d *Detector) familyOfLocked(tokenID string) *family {
	famID, ok := d.tokenFamily[tokenID]
	if !ok {
		return nil
	}
	return d.families[famID]
}

// issueLocked 按全局签发顺序生成新令牌并登记到家族；调用方须持有 d.mu。
func (d *Detector) issueLocked(fam *family, at time.Time) *token {
	d.seq++
	tok := &token{
		id:      fmt.Sprintf("t%d", d.seq),
		expires: minTime(at.Add(d.cfg.L), fam.createdAt.Add(d.cfg.D)),
	}
	fam.tokens[tok.id] = tok
	d.tokenFamily[tok.id] = fam.id
	return tok
}

func (d *Detector) publicToken(tok *token) *Token {
	if tok == nil {
		return nil
	}
	return &Token{ID: tok.id, FamilyID: d.tokenFamily[tok.id], Expires: tok.expires}
}

func (d *Detector) logf(format string, args ...any) {
	if d.cfg.Log == nil {
		return
	}
	fmt.Fprintf(d.cfg.Log, format+"\n", args...)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
