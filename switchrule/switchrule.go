// Package switchrule 实现正常、加严、放宽三档严格度的转移规则状态机。
//
// 每个检验流一个 State。状态机只保有界窗口与 O(1) 计数器：
// Normal 档保留进入以来最近不超过 10 批的初检记录，Tightened 档只保留
// 连续接收数与累计拒收数，因此一次 Record 考察的历史批记录（looked）
// 不超过 10 条，与该流的历史批数无关。每次切换清空全部窗口与计数。
package switchrule

import "ontology/plan"

// Verdict 为一次初检批的判定结果。
type Verdict int

const (
	// Accept 接收：d <= Ac。
	Accept Verdict = iota
	// Reject 拒收：d >= Re。
	Reject
	// Marginal 边缘接收：Ac < d < Re，仅 Reduced 档可能出现；本批接收但触发转移。
	Marginal
)

// entry 为 Normal 档窗口中的一条初检记录。
type entry struct {
	rejected bool
	d        int
}

// State 为单个检验流的转移状态机。不是并发安全的，由调用方串行化。
type State struct {
	sev plan.Severity
	lr  int // 放宽限数 Lr

	hist []entry // Normal：进入以来最近不超过 10 批的初检记录
	lots int     // Normal：进入以来初检批总数

	consec  int // Tightened：最近连续接收批数
	rejects int // Tightened：进入以来累计拒收批数（不要求连续）

	looked int // 累计考察的历史批记录数，用于有界性证明
}

// New 创建初始严格度为 Normal 的状态机，lr 为放宽限数。
func New(lr int) *State {
	return &State{sev: plan.Normal, lr: lr}
}

// Severity 返回当前严格度。
func (s *State) Severity() plan.Severity { return s.sev }

// Record 记录一次初检批判定并按当前严格度执行转移，返回转移后的严格度。
// 切换对下一批生效；每次切换清空该流的全部窗口与计数。
func (s *State) Record(v Verdict, d int) plan.Severity {
	switch s.sev {
	case plan.Normal:
		s.hist = append(s.hist, entry{rejected: v == Reject, d: d})
		if len(s.hist) > 10 {
			s.hist = s.hist[1:]
		}
		s.lots++
		n := len(s.hist)
		rejects5, sum10, allAccept := 0, 0, true
		for i, e := range s.hist {
			s.looked++
			if i >= n-5 && e.rejected {
				rejects5++
			}
			if e.rejected {
				allAccept = false
			} else {
				sum10 += e.d
			}
		}
		switch {
		case rejects5 >= 2:
			s.switchTo(plan.Tightened)
		case s.lots >= 10 && allAccept && sum10 <= s.lr:
			s.switchTo(plan.Reduced)
		}
	case plan.Tightened:
		if v == Reject {
			s.rejects++
			s.consec = 0
			if s.rejects >= 5 {
				s.switchTo(plan.Suspended)
			}
		} else {
			s.consec++
			if s.consec >= 5 {
				s.switchTo(plan.Normal)
			}
		}
	case plan.Reduced:
		if v != Accept {
			s.switchTo(plan.Normal)
		}
	}
	return s.sev
}

// Resume 将 Suspended 的流恢复为 Tightened，并清空全部窗口与计数。
// 调用方负责校验当前确实处于 Suspended。
func (s *State) Resume() {
	s.switchTo(plan.Tightened)
}

// switchTo 切换严格度并清空全部窗口与计数。
func (s *State) switchTo(sev plan.Severity) {
	s.sev = sev
	s.hist = s.hist[:0]
	s.lots, s.consec, s.rejects = 0, 0, 0
}
