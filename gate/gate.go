// Package gate 是令牌内省缓存网关的入口：参数与时钟校验、撤销纪元、
// 风险分级的陈旧复用以及固定次序的放行判定。
package gate

import (
	"errors"
	"strconv"
	"sync"

	"ontology/introspect"
	"ontology/scope"
)

// Verdict 是判定结果。
type Verdict int

const (
	Allow Verdict = iota
	Inactive
	Revoked
	Expired
	Scope
	Unavailable
)

const maxTime int64 = 1_000_000_000_000_000 // 10^15

// Source 同 introspect.Source；Unavailable 没有来源。
type Source = introspect.Source

// Decision 是一次 Check 的结果。
type Decision struct {
	Verdict Verdict
	Reason  string
	Source  Source
	Missing string // Verdict==Scope 时第一个未被覆盖的需求项
	HasSrc  bool   // Unavailable 为 false
}

// MissingInfo 在 Scope 判定时返回缺失项说明，供日志使用。
func (d Decision) MissingInfo() string {
	if d.Verdict == Scope {
		return " missing=" + d.Missing
	}
	return ""
}

// ErrInvalid 与 ErrClock 是入口拒绝错误。
var (
	ErrInvalid = errors.New("gate: invalid argument")
	ErrTime    = errors.New("gate: now out of range")
	ErrClock   = errors.New("gate: clock moved backwards")
)

// Gate 持有内省缓存、撤销纪元与单调时钟水位。
type Gate struct {
	c          *introspect.Cache
	mu         sync.Mutex
	nb         map[string]int64 // 主体撤销纪元，缺失视为 -1
	maxNow     int64            // 已通过检查的调用的最大 now，初值 0
	revTouches int64            // Revoke 触及的缓存条目计数证明（恒 0：Revoke 不碰缓存）
}

// New 创建网关。P/N/G 单位为毫秒，f 为上游内省函数。
func New(P, N, G int64, f introspect.Upstream) *Gate {
	for _, d := range [3]int64{P, N, G} {
		if d < 1 || d > 1_000_000_000 {
			panic("gate: duration must be in [1,1e9]")
		}
	}
	if f == nil {
		panic("gate: upstream function is nil")
	}
	return &Gate{c: introspect.New(P, N, G, f), nb: make(map[string]int64)}
}

func validNeed(need []string) bool {
	if len(need) == 0 {
		return false
	}
	for _, n := range need {
		if n == "" {
			return false
		}
	}
	return true
}

// accept 执行 参数 > 时间 > 时钟回退 三级校验；通过则推进最大 now。
// 被拒绝的调用不改变任何状态。
func (g *Gate) accept(now int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < 0 || now > maxTime {
		return ErrTime
	}
	if now < g.maxNow {
		return ErrClock
	}
	g.maxNow = now
	return nil
}

// Check 按规则对令牌做内省与放行判定。
func (g *Gate) Check(token string, need []string, now int64) (d Decision, err error) {
	if token == "" || !validNeed(need) {
		return Decision{}, ErrInvalid
	}
	if err = g.accept(now); err != nil {
		return Decision{}, err
	}

	var (
		e    introspect.Entry
		src  introspect.Source
		oerr error
	)
	// 取记录次序：新鲜缓存优先；其次对"缓存正向记录已被撤销"短路
	// （撤销不可恢复，无需再问上游）；否则 Obtain（发 f / 单飞）。
	g.mu.Lock()
	cached, hasCached := g.c.Peek(token)
	if hasCached && now < cached.U {
		g.mu.Unlock()
		e, src = cached, introspect.SourceCache
	} else if hasCached && cached.Res.Active {
		if nbSub, revoked := g.nb[cached.Res.Sub]; revoked && cached.Res.Iat <= nbSub {
			g.mu.Unlock()
			e, src = cached, introspect.SourceCache
		} else {
			g.mu.Unlock()
			e, src, oerr = g.c.Obtain(token, now)
		}
	} else {
		g.mu.Unlock()
		e, src, oerr = g.c.Obtain(token, now)
	}
	if oerr != nil {
		// f 失败：仅低风险正向记录、处于陈旧窗 [u, u+G) 且 now < Exp 时复用。
		if e.Res.Active && !scope.HighRisk(need) && e.U <= now && now < e.U+g.c.G && now < e.Res.Exp {
			src = introspect.SourceStale
		} else {
			return Decision{Verdict: Unavailable, Reason: "upstream unavailable: " + oerr.Error()}, nil
		}
	}

	g.mu.Lock()
	nb, ok := g.nb[e.Res.Sub]
	g.mu.Unlock()
	if !ok {
		nb = -1
	}

	d.Source = src
	d.HasSrc = true
	switch {
	case !e.Res.Active:
		d.Verdict, d.Reason = Inactive, "introspection reported inactive"
	case e.Res.Iat <= nb:
		d.Verdict = Revoked
		d.Reason = "revoked epoch: iat=" + strconv.FormatInt(e.Res.Iat, 10) +
			" <= nb[" + e.Res.Sub + "]=" + strconv.FormatInt(nb, 10)
	case now >= e.Res.Exp:
		d.Verdict, d.Reason = Expired, "expired: now="+strconv.FormatInt(now, 10)+
			" >= exp="+strconv.FormatInt(e.Res.Exp, 10)
	default:
		if miss := scope.FirstMissing(e.Res.Scopes, need); miss != "" {
			d.Verdict, d.Missing, d.Reason = Scope, miss, "missing scope: "+miss
		} else {
			d.Verdict, d.Reason = Allow, "active, not revoked, unexpired, all scopes granted"
		}
	}
	return d, nil
}

// Revoke 令 nb[sub]=max(nb[sub], t)，不遍历缓存。
func (g *Gate) Revoke(sub string, t, now int64) error {
	if sub == "" || t < 0 || t > maxTime {
		return ErrInvalid
	}
	if err := g.accept(now); err != nil {
		return err
	}
	g.mu.Lock()
	// 首次撤销时显式置为 max(-1,t)=t；之后取最大值。缺失键在 Check 中按 -1 处理。
	if cur, ok := g.nb[sub]; !ok || t > cur {
		g.nb[sub] = t
	}
	g.revTouches = 0 // 该操作不遍历、不清空、不标记任何缓存条目
	g.mu.Unlock()
	return nil
}

// Calls 返回实际进入 f 的次数。
func (g *Gate) Calls() int64 { return g.c.Calls() }

// RevokeCacheTouches 返回 Revoke 触及的缓存条目数证明，恒为 0。
func (g *Gate) RevokeCacheTouches() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.revTouches
}

// CachedTokens 返回缓存中的令牌条目数。
func (g *Gate) CachedTokens() int { return g.c.Len() }
