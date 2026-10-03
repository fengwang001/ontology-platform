package limiter

import (
	"errors"
	"sync"

	"ontology/rule"
	"ontology/window"
)

// 参数与状态错误。调用方可用 errors.Is 判别类别。
var (
	ErrInvalid     = errors.New("limiter: invalid argument")
	ErrExists      = errors.New("limiter: rule id already exists")
	ErrDuplicate   = errors.New("limiter: identical pattern already exists")
	ErrNotFound    = errors.New("limiter: rule id not found")
	ErrTime        = errors.New("limiter: now out of range")
	ErrClockGoBack = errors.New("limiter: clock moved backwards")
)

// Decision 是一次 Allow 的判定结果。
type Decision struct {
	Allowed      bool
	RejectedBy   string   // 导致整体拒绝的强制规则 id（字节序最小），空表示无
	Selected     []string // 选中的规则 id，按字节序
	ShadowReject []string // 本次超限的影子规则 id，按字节序
}

// Limiter 是并发安全的描述符分级限流器。零值不可用，请用 New。
type Limiter struct {
	mu     sync.RWMutex
	rules  map[string]*rule.Rule
	sigs   map[string]string // 规则 id -> 模式签名
	table  *window.Table
	shadow map[string]int64 // 影子超限累计 ShadowReject[id]
	maxNow int64
}

// New 创建空限流器。
func New() *Limiter {
	return &Limiter{
		rules:  make(map[string]*rule.Rule),
		sigs:   make(map[string]string),
		table:  window.NewTable(),
		shadow: make(map[string]int64),
	}
}

// AddRule 添加规则。错误优先级：参数非法 > id 已存在 > 模式重复。
func (l *Limiter) AddRule(id string, pattern []rule.Pair, limit, windowMS int64, mode rule.Mode) error {
	r, err := rule.ValidateRule(id, pattern, limit, windowMS, mode)
	if err != nil {
		return ErrInvalid
	}
	sig := rule.PatternSig(pattern)
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.rules[id]; ok {
		return ErrExists
	}
	for _, existing := range l.sigs {
		if existing == sig {
			return ErrDuplicate
		}
	}
	l.rules[id] = r
	l.sigs[id] = sig
	return nil
}

// SetMode 切换规则模式，不重置计数器；id 不存在报 ErrNotFound。
func (l *Limiter) SetMode(id string, mode rule.Mode) error {
	if !rule.ValidMode(mode) {
		return ErrInvalid
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.rules[id]
	if !ok {
		return ErrNotFound
	}
	r.Mode = mode
	return nil
}

// RemoveRule 删除规则及其全部计数器；id 不存在报 ErrNotFound。
func (l *Limiter) RemoveRule(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.rules[id]; !ok {
		return ErrNotFound
	}
	delete(l.rules, id)
	delete(l.sigs, id)
	delete(l.shadow, id)
	l.table.DeleteRule(id)
	return nil
}

// Allow 对描述符在 now（毫秒）时刻做限流判定。
// 错误优先级：描述符非法 > now 越界 > 时钟回退。
func (l *Limiter) Allow(desc rule.Descriptor, now int64) (Decision, error) {
	if err := rule.ValidateDescriptor(desc); err != nil {
		return Decision{}, ErrInvalid
	}
	if now < 0 || now > 1_000_000_000_000_000 {
		return Decision{}, ErrTime
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if now < l.maxNow {
		return Decision{}, ErrClockGoBack
	}
	// 通过全部检查：无论后续放行与否，该 Allow 都已执行，推进时钟。
	l.maxNow = now

	matched := make([]*rule.Rule, 0)
	for _, r := range l.rules {
		if r.Match(desc) {
			matched = append(matched, r)
		}
	}
	selected := rule.Select(matched)
	d := Decision{Allowed: true, Selected: make([]string, 0, len(selected))}
	for _, r := range selected {
		d.Selected = append(d.Selected, r.ID)
	}
	if len(selected) == 0 {
		return d, nil
	}

	// 阶段一：对所选规则做只读估计（Estimate 会滚动内存计数器，
	// 滚动本身是幂等的时间归一化，不加计数；随后阶段二从同一 now 重做）。
	type sel struct {
		r   *rule.Rule
		key window.Key
		est int64
	}
	items := make([]sel, len(selected))
	for i, r := range selected {
		key := window.Key{RuleID: r.ID, Values: r.Values(desc)}
		items[i] = sel{r: r, key: key, est: l.table.Estimate(key, now, r.W)}
	}

	// 任一强制规则不放行 => 整体拒绝，不推进任何计数器、不记影子。
	rejectID := ""
	for _, it := range items {
		if it.r.Mode == rule.Enforce && it.est+1 > it.r.L {
			if rejectID == "" || it.r.ID < rejectID {
				rejectID = it.r.ID
			}
		}
	}
	if rejectID != "" {
		d.Allowed = false
		d.RejectedBy = rejectID
		d.ShadowReject = []string{}
		return d, nil
	}

	// 阶段二：全部强制放行 => 所选各规则 cur+1（封顶 L+1），
	// 影子超限者记录 ShadowReject。
	d.ShadowReject = []string{}
	for _, it := range items {
		l.table.AllowIncr(it.key, now, it.r.W, it.r.L+1)
		if it.r.Mode == rule.Shadow && it.est+1 > it.r.L {
			l.shadow[it.r.ID]++
			d.ShadowReject = append(d.ShadowReject, it.r.ID)
		}
	}
	return d, nil
}

// Tracked 返回计数器总数。
func (l *Limiter) Tracked() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.table.Len()
}

// ShadowReject 返回某影子规则累计的超限次数。
func (l *Limiter) ShadowRejectCount(id string) int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.shadow[id]
}
