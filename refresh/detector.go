// Package refresh 实现刷新令牌轮换与重用检测。
//
// 每个登录会话构成一个"家族"(family)，续期时签发新令牌并换出旧令牌。
// 被换出令牌在宽限期内的再次出示视为客户端重试；超过宽限或后继已被
// 再次续期则判定为盗用，立即吊销整个家族。
package refresh

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"
)

// 配置校验错误：构造检测器时整体拒绝，并给出可区分的原因。
var (
	// ErrNonPositiveL 寿命 L 非正。
	ErrNonPositiveL = errors.New("refresh: lifetime L must be positive")
	// ErrNonPositiveD 绝对寿命 D 非正。
	ErrNonPositiveD = errors.New("refresh: absolute lifetime D must be positive")
	// ErrNegativeG 宽限 G 为负。
	ErrNegativeG = errors.New("refresh: grace G must not be negative")
	// ErrGraceTooLarge 宽限 G 不小于寿命 L。
	ErrGraceTooLarge = errors.New("refresh: grace G must be smaller than lifetime L")
	// ErrDTooSmall 绝对寿命 D 小于寿命 L。
	ErrDTooSmall = errors.New("refresh: absolute lifetime D must not be smaller than lifetime L")
)

// 出示令牌被拒绝的原因，按固定优先级只报第一个。
var (
	// ErrUnknownToken 令牌未知（从未签发或不属于任何家族）。
	ErrUnknownToken = errors.New("refresh: unknown token")
	// ErrFamilyRevoked 令牌所属家族已被吊销。
	ErrFamilyRevoked = errors.New("refresh: token family revoked")
	// ErrFamilyExpired 家族已达到绝对寿命 D。
	ErrFamilyExpired = errors.New("refresh: token family reached absolute lifetime")
	// ErrTokenExpired 家族当前令牌已到期（到期时刻起失效）。
	ErrTokenExpired = errors.New("refresh: current token expired")
	// ErrReuseDetected 出示已换出令牌且不满足重试条件，判定盗用。
	ErrReuseDetected = errors.New("refresh: token reuse detected, family revoked")
)

// Token 是刷新令牌标识，形如 t1、t2……，由检测器按全局签发顺序生成。
type Token string

// Entry 描述一次令牌出示（续期）的输入。
type Entry struct {
	// Token 客户端出示的刷新令牌。
	Token Token
	// At 出示时刻。
	At time.Time
}

// Result 描述一次令牌出示（续期）的输出。
type Result struct {
	// Issued 本次是否签发了新令牌。重试与被拒绝时为 false。
	Issued bool
	// Token 成功时可用的令牌：续期为新令牌，重试为原后继令牌。
	Token Token
	// ExpiresAt 该令牌的到期时刻（重试时仍报告后继的到期时刻）。
	ExpiresAt time.Time
}

// Detector 是刷新令牌轮换与重用检测器，所有方法可并发调用。
type Detector struct {
	mu sync.Mutex

	lifetime time.Duration
	absolute time.Duration
	grace    time.Duration
	log      *slog.Logger

	nextID int

	families map[int]*family
	tokens   map[Token]*tokenInfo
}

type family struct {
	id       int
	created  time.Time
	absolute time.Time
	current  Token
	revoked  bool
}

type tokenInfo struct {
	family    int
	expiresAt time.Time
	rotated   bool
	rotatedAt time.Time
	successor Token
}

// Option 配置检测器的可选项。
type Option func(*Detector)

// WithLogger 设置判定日志输出位置；nil 表示不输出日志。
func WithLogger(w io.Writer) Option {
	return func(d *Detector) {
		if w == nil {
			d.log = nil
			return
		}
		d.log = slog.New(slog.NewTextHandler(w, nil))
	}
}

// New 创建检测器。L 为令牌寿命，D 为家族绝对寿命，G 为重试宽限。
// 配置不合法时返回对应的可区分错误，不产生任何检测器。
func New(L, D time.Duration, G time.Duration, opts ...Option) (*Detector, error) {
	if L <= 0 {
		return nil, ErrNonPositiveL
	}
	if D <= 0 {
		return nil, ErrNonPositiveD
	}
	if G < 0 {
		return nil, ErrNegativeG
	}
	if G >= L {
		return nil, ErrGraceTooLarge
	}
	if D < L {
		return nil, ErrDTooSmall
	}
	d := &Detector{
		lifetime: L,
		absolute: D,
		grace:    G,
		log:      slog.New(slog.NewTextHandler(os.Stderr, nil)),
		families: make(map[int]*family),
		tokens:   make(map[Token]*tokenInfo),
	}
	for _, opt := range opts {
		opt(d)
	}
	return d, nil
}

// Login 创建新家族并签发首个令牌，到期时刻取 now+L 与 创建时刻+D 的较小者。
func (d *Detector) Login(now time.Time) (Token, time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()

	fam := &family{
		id:       len(d.families) + 1,
		created:  now,
		absolute: now.Add(d.absolute),
	}
	tok, expires := d.issueLocked(fam, now)
	d.families[fam.id] = fam

	d.logLocked("login", "issued", slog.Group("family",
		slog.Int("id", fam.id),
		slog.Time("created", fam.created),
		slog.Time("absolute_expiry", fam.absolute)),
		slog.String("token", string(tok)), slog.Time("expires_at", expires))
	return tok, expires
}

// Renew 出示令牌续期。
func (d *Detector) Renew(e Entry) (Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	reject := func(reason error, why string, args ...any) (Result, error) {
		logArgs := append([]any{
			slog.String("token", string(e.Token)),
			slog.Time("at", e.At),
			slog.String("reason", reason.Error()),
			slog.String("why", why),
		}, args...)
		d.logLocked("renew", "rejected", logArgs...)
		return Result{}, reason
	}

	info, ok := d.tokens[e.Token]
	if !ok {
		// 原因优先级 1：令牌未知。
		return reject(ErrUnknownToken, "token was never issued")
	}
	fam := d.families[info.family]
	if fam.revoked {
		// 原因优先级 2：家族已吊销。
		return reject(ErrFamilyRevoked, "family already revoked",
			slog.Int("family", fam.id))
	}
	if !e.At.Before(fam.absolute) {
		// 原因优先级 3：家族已达绝对寿命（到期时刻起失效，不改变状态）。
		return reject(ErrFamilyExpired, "at >= family absolute expiry",
			slog.Int("family", fam.id), slog.Time("absolute_expiry", fam.absolute))
	}

	if info.rotated {
		// 已换出令牌不检查自身到期；只区分重试与盗用。
		elapsed := e.At.Sub(info.rotatedAt)
		switch {
		case elapsed < 0:
			// 出示时刻早于换出时刻（时钟回拨），不可能是合法重试。
			d.revokeLocked(fam)
			return reject(ErrReuseDetected, "present time before rotated_at",
				slog.Int("family", fam.id), slog.Duration("elapsed", elapsed))
		case elapsed < d.grace && info.successor == fam.current:
			// 宽限内且后继仍是家族当前令牌：客户端重试，原样返回后继。
			succ := d.tokens[info.successor]
			res := Result{Token: info.successor, ExpiresAt: succ.expiresAt}
			d.logLocked("renew", "retry",
				slog.String("token", string(e.Token)),
				slog.Time("at", e.At),
				slog.Int("family", fam.id),
				slog.Duration("elapsed", elapsed),
				slog.Duration("grace", d.grace),
				slog.String("successor", string(info.successor)),
				slog.Time("expires_at", res.ExpiresAt))
			return res, nil
		default:
			// 超过宽限，或后继已被再次续期：盗用，吊销整个家族。
			d.revokeLocked(fam)
			return reject(ErrReuseDetected, "rotated token outside retry window",
				slog.Int("family", fam.id), slog.Duration("elapsed", elapsed),
				slog.Duration("grace", d.grace),
				slog.Bool("successor_current", info.successor == fam.current))
		}
	}

	if e.Token != fam.current {
		// 未换出却不是当前令牌：内部不应出现，按盗用处理以防不一致状态被利用。
		d.revokeLocked(fam)
		return reject(ErrReuseDetected, "token not current",
			slog.Int("family", fam.id))
	}
	if !e.At.Before(info.expiresAt) {
		// 原因优先级 4：当前令牌已到期（到期时刻起失效）。
		return reject(ErrTokenExpired, "at >= token expiry",
			slog.Int("family", fam.id), slog.Time("expires_at", info.expiresAt))
	}

	// 续期：当前令牌换出，签发后继。
	info.rotated = true
	info.rotatedAt = e.At
	tok, expires := d.issueLocked(fam, e.At)
	info.successor = tok

	d.logLocked("renew", "rotated",
		slog.String("token", string(e.Token)),
		slog.Time("at", e.At),
		slog.Int("family", fam.id),
		slog.String("successor", string(tok)),
		slog.Time("expires_at", expires))
	return Result{Issued: true, Token: tok, ExpiresAt: expires}, nil
}

// Logout 按令牌所在家族主动登出（吊销整个家族）。
func (d *Detector) Logout(t Token) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	info, ok := d.tokens[t]
	if !ok {
		d.logLocked("logout", "rejected",
			slog.String("token", string(t)),
			slog.String("reason", ErrUnknownToken.Error()),
			slog.String("why", "token was never issued"))
		return ErrUnknownToken
	}
	fam := d.families[info.family]
	already := fam.revoked
	fam.revoked = true

	d.logLocked("logout", "revoked",
		slog.String("token", string(t)),
		slog.Int("family", fam.id),
		slog.Bool("already_revoked", already))
	return nil
}

// issueLocked 签发新令牌并更新家族当前令牌；调用方须持有 d.mu。
func (d *Detector) issueLocked(fam *family, now time.Time) (Token, time.Time) {
	d.nextID++
	tok := Token("t" + strconv.Itoa(d.nextID))
	expires := now.Add(d.lifetime)
	if abs := fam.absolute; abs.Before(expires) {
		expires = abs
	}
	d.tokens[tok] = &tokenInfo{family: fam.id, expiresAt: expires}
	fam.current = tok
	return tok, expires
}

// revokeLocked 吊销家族，使其所有令牌立即不可用；调用方须持有 d.mu。
func (d *Detector) revokeLocked(fam *family) {
	fam.revoked = true
	d.logLocked("family", "revoked",
		slog.Int("family", fam.id),
		slog.String("reason", ErrReuseDetected.Error()))
}

// logLocked 打印一条输入/输出/判定依据日志；未配置日志器时为空操作。
func (d *Detector) logLocked(op, decision string, args ...any) {
	if d.log == nil {
		return
	}
	all := append([]any{slog.String("op", op), slog.String("decision", decision)}, args...)
	d.log.Info("refresh token decision", all...)
}
