package ncd

import "sort"

const (
	// policyYearDays 保单年度长度：生效日起 365 天，左闭右开。
	policyYearDays = 365
	// earlyRenewalDays 续保窗口左端：到期日前 30 天（含）。
	earlyRenewalDays = 30
)

// claimRec 一次出险登记。责任比例 0..100，不小于门槛即有责。
type claimRec struct {
	id    string
	day   int // 事故日
	ratio int // 责任比例
}

// yearRec 一个保单年度的定级相关状态。
type yearRec struct {
	start     int        // 年度生效日（含），年度为 [start, start+365)
	level     int        // 该年度裁定等级（首年/中断后首年恒为 0）
	base      bool       // 是否为链条首年（首次投保或中断清零），等级不参与重算
	protected bool       // 该年度是否已购买等级保护
	liable    int        // 该年度有责出险次数（随登记/撤销增量维护）
	claims    []claimRec // 事故日落在本年度的出险
}

// Surcharge 保费差额追补记录：重定级使某次续保等级降低时产生。
type Surcharge struct {
	YearIndex int   // 被重定级的保单年度下标
	YearStart int   // 该年度生效日
	OldLevel  int   // 原等级
	NewLevel  int   // 重定级后的新等级
	Amount    int64 // 新等级保费减原等级保费（>0）
}

// PolicyRecord 保单审计记录（转移/新保时留痕）。
type PolicyRecord struct {
	PolicyID string
	FromDay  int
	ToDay    int // -1 表示当前在保
	ClosedBy string
}

// insured 一个被保人的全部定级状态。
type insured struct {
	years      []yearRec      // 按 start 严格递增、互不重叠
	claimYear  map[string]int // 事故编号 -> 所属年度下标
	surcharges []Surcharge
	policies   []PolicyRecord
}

func newInsured() *insured {
	return &insured{claimYear: make(map[string]int)}
}

// findYear 返回事故日所属保单年度下标，未承保返回 -1。二分查找，O(log n)。
func (in *insured) findYear(day int) int {
	i := sort.Search(len(in.years), func(i int) bool { return in.years[i].start > day }) - 1
	if i >= 0 && day < in.years[i].start+policyYearDays {
		return i
	}
	return -1
}

// closePolicy 终止当前在保保单（转移或中断后重新投保时调用）。
func (in *insured) closePolicy(day int, by string) {
	if n := len(in.policies); n > 0 && in.policies[n-1].ToDay < 0 {
		in.policies[n-1].ToDay = day
		in.policies[n-1].ClosedBy = by
	}
}

// recompute 迟报/撤销/补购保护后的追溯重定级：
// 只重算 fromYear 之后（不含）的年度；首年等级固定为 0。
// 等级降低产生追补记录，升高不退费但更新在案等级。
// steps 用于统计访问的年度记录数，验证重定级范围与续保 O(1)。
func (e *Engine) recompute(in *insured, fromYear int) {
	for i := fromYear + 1; i < len(in.years); i++ {
		e.steps++
		newLevel := 0
		if !in.years[i].base {
			prev := &in.years[i-1]
			newLevel = gradeNext(prev.level, prev.liable, prev.protected, e.cfg.MaxLevel)
		}
		old := in.years[i].level
		if newLevel < old {
			in.surcharges = append(in.surcharges, Surcharge{
				YearIndex: i,
				YearStart: in.years[i].start,
				OldLevel:  old,
				NewLevel:  newLevel,
				Amount:    e.cfg.Premiums[newLevel] - e.cfg.Premiums[old],
			})
		}
		in.years[i].level = newLevel
	}
}
