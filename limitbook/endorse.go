package limitbook

// 批改在指定生效日改变某成员个人年度限额、某项目年度项目限额或
// 家庭共享年度限额，只对生效日所在保单年度及之后的年度生效，不追溯
// 已结算的理赔。生效日早于当前活跃理赔中最晚发生日时拒绝（追溯批改），
// 等于该发生日则允许。同一对象同一生效日的两次批改以后一次为准。

func (p *Policy) endorseMemberAnnual(memberID string, effDay, newLimit int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkEffDay(effDay); err != nil {
		return err
	}
	m, ok := p.members[memberID]
	if !ok {
		return newErr(ErrMemberNotFound, "成员 %s", memberID)
	}
	if effDay < p.maxDay {
		return newErr(ErrRetroactive, "生效日 %d 早于已受理理赔最晚发生日 %d", effDay, p.maxDay)
	}
	m.annual.set(p.yearOf(effDay), newLimit)
	return nil
}

func (p *Policy) endorseItemAnnual(itemID string, effDay, newLimit int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkEffDay(effDay); err != nil {
		return err
	}
	it, ok := p.items[itemID]
	if !ok {
		return newErr(ErrItemNotFound, "项目 %s", itemID)
	}
	if effDay < p.maxDay {
		return newErr(ErrRetroactive, "生效日 %d 早于已受理理赔最晚发生日 %d", effDay, p.maxDay)
	}
	it.annual.set(p.yearOf(effDay), newLimit)
	return nil
}

func (p *Policy) endorseFamilyAnnual(effDay, newLimit int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkEffDay(effDay); err != nil {
		return err
	}
	if effDay < p.maxDay {
		return newErr(ErrRetroactive, "生效日 %d 早于已受理理赔最晚发生日 %d", effDay, p.maxDay)
	}
	p.family.set(p.yearOf(effDay), newLimit)
	return nil
}

// checkEffDay 校验依赖保单上下文的生效日参数：早于承保起始日无对应
// 保单年度，视为参数非法。
func (p *Policy) checkEffDay(effDay int64) error {
	if effDay < p.start {
		return newErr(ErrInvalidParam, "生效日 %d 早于承保起始日 %d", effDay, p.start)
	}
	return nil
}
