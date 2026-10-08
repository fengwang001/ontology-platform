package reconcile

// Verdict 是一次属性裁决的结果。
type Verdict int

const (
	// VerdictResolved 裁决出唯一保留取值。
	VerdictResolved Verdict = iota
	// VerdictTie 最高优先级上出现并列的不同取值，不可和解。
	VerdictTie
)

// adjudicate 按固定不变的优先规则裁决一组候选取值：
//
//  1. 候选按副本自身携带的 Priority 比较，高者胜出；
//  2. 若最高优先级上的所有候选取值一致，则该取值唯一保留；
//  3. 若最高优先级上出现两个及以上不同取值（可比较标识本身
//     并列，且规则没有更细一层的比较依据），判定为并列，
//     不退回依据到达顺序等与副本内容无关的方式强行裁决。
//
// 规则只依赖候选集合本身，与候选数量、到达顺序无关。
// 返回的 comparisons 是本次裁决实际执行的优先级比较次数，
// 用于复核裁决开销只与冲突规模相关。
func adjudicate(cands []Candidate) (winner Candidate, verdict Verdict, comparisons int) {
	top := cands[0]
	for _, c := range cands[1:] {
		comparisons++
		if c.Priority > top.Priority {
			top = c
		}
	}

	winner = top
	for _, c := range cands {
		if c.Priority == top.Priority && c.Value != top.Value {
			return Candidate{}, VerdictTie, comparisons
		}
	}
	return winner, VerdictResolved, comparisons
}
