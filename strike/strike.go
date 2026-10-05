// Package strike 维护创作者违规计分，并以纯函数形式给出账号状态。
//
// 每条生效决定产生一条计分记录，权重为 level-1，有效期 [t, t+P)。
// 计分记录按过期时刻非递减排列（系统时钟单调保证），State 查询用
// 平行过期时刻切片二分定位首条有效记录，再顺序汇总；非导出计数器
// touched 只统计汇总循环触碰的记录结构体数，因此单次 State 触碰数
// 不超过此刻有效期内的计分条数 + 2，与已过期历史条数、其他创作者
// 的记录数均无关。
package strike

import "sort"

// State 是创作者账号状态，为有效计分之和 s 的纯函数（不粘滞）。
type State int

const (
	Normal State = iota // s < 3，正常
	Muted               // 3 <= s < 5，禁言
	Banned              // s >= 5，封禁
)

func (s State) String() string {
	switch s {
	case Muted:
		return "禁言"
	case Banned:
		return "封禁"
	default:
		return "正常"
	}
}

// record 是一条计分记录。live 为假表示所属决定已被推翻，计分自始不计。
type record struct {
	weight int
	live   bool
}

// Ledger 是计分账本。Add 的 now 必须全局非递减（由门面时钟保证）。
type Ledger struct {
	p       int64
	recs    map[string][]*record // 按创作者，与 exp 平行，过期时刻非递减
	exp     map[string][]int64   // 每条记录的过期时刻 t+P
	byDec   map[string]*record   // 决定 id -> 记录，用于推翻时撤销
	touched int                  // 最近一次 Score 触碰的记录结构体数
}

// NewLedger 返回计分有效期为 p 的空账本。
func NewLedger(p int64) *Ledger {
	return &Ledger{
		p:     p,
		recs:  make(map[string][]*record),
		exp:   make(map[string][]int64),
		byDec: make(map[string]*record),
	}
}

// Add 为创作者记入一条权重为 weight、在 [now, now+P) 内有效的计分。
// 权重 <= 0（level 1）直接不记。
func (l *Ledger) Add(decisionID, creator string, now int64, weight int) {
	if weight <= 0 {
		return
	}
	r := &record{weight: weight, live: true}
	l.recs[creator] = append(l.recs[creator], r)
	l.exp[creator] = append(l.exp[creator], now+l.p)
	l.byDec[decisionID] = r
}

// Remove 撤销某决定对应的计分（推翻时调用），自始不计。
func (l *Ledger) Remove(decisionID string) {
	if r, ok := l.byDec[decisionID]; ok {
		r.live = false
		delete(l.byDec, decisionID)
	}
}

// Score 返回创作者此刻有效且未推翻的计分之和。
// 二分只读过期时刻数组，不触碰记录结构体；touched 只统计汇总循环。
func (l *Ledger) Score(creator string, now int64) int {
	l.touched = 0
	exp := l.exp[creator]
	first := sort.Search(len(exp), func(i int) bool { return exp[i] > now })
	sum := 0
	for _, r := range l.recs[creator][first:] {
		l.touched++
		if r.live {
			sum += r.weight
		}
	}
	return sum
}

// State 返回创作者此刻的账号状态，是 Score 的纯函数。
func (l *Ledger) State(creator string, now int64) State {
	switch s := l.Score(creator, now); {
	case s >= 5:
		return Banned
	case s >= 3:
		return Muted
	default:
		return Normal
	}
}

// Touched 返回最近一次 Score 调用触碰的计分记录结构体数（供测试断言上界）。
func (l *Ledger) Touched() int {
	return l.touched
}
