package ontology

import "sort"

// endorsements 记录同一对象的批改序列，按生效年度升序保存。
// 批改对生效日所在保单年度及之后年度生效，因此某年度的有效限额
// 是「生效年度 <= 该年度」的最后一条批改的值；同一对象同一年度
// （含同一生效日）的多次批改以后一次为准（注册次序覆盖）。
type endorsements struct {
	years  []int64
	values []int64
}

func (e *endorsements) set(year, value int64) {
	i := sort.Search(len(e.years), func(i int) bool { return e.years[i] >= year })
	if i < len(e.years) && e.years[i] == year {
		e.values[i] = value
		return
	}
	e.years = append(e.years, 0)
	copy(e.years[i+1:], e.years[i:])
	e.years[i] = year
	e.values = append(e.values, 0)
	copy(e.values[i+1:], e.values[i:])
	e.values[i] = value
}

// get 返回 year 年度的有效限额；无适用批改时返回 base。
func (e *endorsements) get(year, base int64) int64 {
	i := sort.Search(len(e.years), func(i int) bool { return e.years[i] > year }) - 1
	if i < 0 {
		return base
	}
	return e.values[i]
}

// endorse 在指定生效日调整某层限额。调用方须持有 p.mu。
func (p *Policy) endorse(kind EndorseKind, objectID string, effectiveDay, newLimit int64) *Error {
	if effectiveDay < 0 || effectiveDay < p.startDay || newLimit < 0 {
		return errOf(ErrInvalidParam, p.id, "批改参数非法")
	}
	var target *endorsements
	switch kind {
	case EndorseMemberAnnual:
		m, ok := p.members[objectID]
		if !ok {
			return errOf(ErrMemberNotFound, p.id, objectID)
		}
		target = &m.endorse
	case EndorseItemAnnual:
		it, ok := p.items[objectID]
		if !ok {
			return errOf(ErrItemNotFound, p.id, objectID)
		}
		target = &it.endorse
	case EndorseFamilyAnnual:
		target = &p.familyEndors
	default:
		return errOf(ErrInvalidParam, p.id, "未知批改类型")
	}
	if maxDay, ok := p.days.max(); ok && effectiveDay < maxDay {
		return errOf(ErrRetroactive, p.id, "生效日早于已受理理赔最晚发生日")
	}
	target.set(p.yearOf(effectiveDay), newLimit)
	return nil
}
