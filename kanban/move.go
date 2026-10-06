package kanban

// checkClock 校验 now 范围与时钟单调。
func (b *Board) checkClock(now int64) error {
	if now < 0 || now > maxNow {
		return newError(ErrInvalidArgument, "now %d out of [0,1e12]", now)
	}
	if now < b.lastNow {
		return newError(ErrClockRollback, "now %d < last accepted now %d", now, b.lastNow)
	}
	return nil
}

// resolveOp 统一执行：参数(时钟) > 时钟回退 > 卡片不存在 > 版本冲突 的前置校验。
func (b *Board) resolveOp(cardID string, expectVer int, now int64) (*card, error) {
	if err := b.checkClock(now); err != nil {
		return nil, err
	}
	c, ok := b.cards[cardID]
	if !ok {
		return nil, newError(ErrCardNotFound, "card %q does not exist", cardID)
	}
	if expectVer != c.version {
		return nil, newError(ErrVersionConflict, "expect version %d but card %q is %d", expectVer, cardID, c.version)
	}
	return c, nil
}

func (b *Board) prereqIncomplete(c *card) (string, bool) {
	// 仅遍历该卡片自己的前置集合：开销 O(卡片自己的前置数)。
	for p := range c.prereqs {
		pc := b.cards[p]
		if pc == nil || pc.col != len(b.columns)-1 {
			return p, true
		}
	}
	return "", false
}

// Move 移动卡片。
// 向右只能到紧邻下一列；向左可到任一更左列；完成列卡片不可移动。
// expedite=true 表示该次移入进行中列申请/保留加急，豁免列上限与负责人上限。
func (b *Board) Move(user, cardID string, to, expectVer int, expedite bool, now int64) (*Card, error) {
	if to < 0 || to >= len(b.columns) {
		return nil, newError(ErrInvalidArgument, "target column %d out of range", to)
	}
	c, err := b.resolveOp(cardID, expectVer, now)
	if err != nil {
		return nil, err
	}

	from := c.col
	lastCol := len(b.columns) - 1

	// 流转合法性。
	switch {
	case from == lastCol:
		return nil, newError(ErrIllegalFlow, "card %q is in done column; reopen first", cardID)
	case to == from:
		return nil, newError(ErrIllegalFlow, "card %q already in column %d", cardID, to)
	case to > from && to != from+1:
		return nil, newError(ErrIllegalFlow, "rightward move must be to the adjacent column (from %d to %d)", from, to)
	}

	targetInProgress := b.isInProgress(to)
	if expedite && !targetInProgress {
		return nil, newError(ErrInvalidArgument, "expedite is only allowed when moving into an in-progress column")
	}

	// 离开待办（进入进行中或完成）时，全部前置必须在完成列。
	if from == 0 && to != 0 {
		if p, incomplete := b.prereqIncomplete(c); incomplete {
			return nil, newError(ErrDependency, "prerequisite %q is not done", p)
		}
	}

	// 加急：进行中区域任一时刻至多 1 张。已持有标记的卡片可随移动保留。
	grantExpedite := false
	if expedite {
		switch {
		case c.expedited:
			grantExpedite = true
		case b.expeditedCard != "" && b.expeditedCard != c.id:
			return nil, newError(ErrExpediteBusy, "another expedited card %q is in progress", b.expeditedCard)
		default:
			grantExpedite = true
		}
	}

	// 容量：先离开释放（含计数），再对目标列判定。
	fromInProgress := b.isInProgress(from)
	if fromInProgress {
		b.colCount[from]--
		b.ownerCount[c.owner]--
	} else {
		b.colCount[from]--
	}

	ownerInc := 0
	if targetInProgress {
		ownerInc = 1
	}
	if !grantExpedite && targetInProgress {
		if lim := b.columns[to].wipLimit; lim != LimitUnlimited && b.colCount[to]+1 > lim {
			b.rollbackPlacement(c, from, fromInProgress)
			return nil, newError(ErrColumnFull, "column %d is full (limit %d, occupancy %d)", to, lim, b.colCount[to])
		}
		if b.ownerCount[c.owner]+ownerInc > b.ownerLimit {
			b.rollbackPlacement(c, from, fromInProgress)
			return nil, newError(ErrOwnerFull, "owner %q is at WIP limit %d", c.owner, b.ownerLimit)
		}
	}

	// 提交。
	if targetInProgress {
		b.colCount[to]++
		b.ownerCount[c.owner]++
	} else {
		b.colCount[to]++
	}
	c.col = to
	c.version++
	if grantExpedite {
		c.expedited = true
		b.expeditedCard = c.id
	}
	if !targetInProgress && c.expedited {
		// 离开进行中区域（回到待办或进入完成）清除加急标记。
		c.expedited = false
		b.expeditedCard = ""
	}
	b.lastNow = now
	return b.snapshot(c), nil
}

// rollbackPlacement 在容量判定失败后恢复离开列的占用（此时尚未写入 c.col）。
func (b *Board) rollbackPlacement(c *card, from int, fromInProgress bool) {
	b.colCount[from]++
	if fromInProgress {
		b.ownerCount[c.owner]++
	}
}

// Reopen 把完成列卡片移回最后一个进行中阶段，受全部上限与依赖约束。
func (b *Board) Reopen(user, cardID string, expectVer int, now int64) (*Card, error) {
	c, err := b.resolveOp(cardID, expectVer, now)
	if err != nil {
		return nil, err
	}
	lastCol := len(b.columns) - 1
	if c.col != lastCol {
		return nil, newError(ErrIllegalFlow, "card %q is not in done column", cardID)
	}
	// 任一后继处于进行中或完成列，则被依赖，不能 reopen。
	for succ := range b.successors[c.id] {
		sc := b.cards[succ]
		if sc != nil && sc.col != 0 {
			return nil, newError(ErrDependency, "card %q is depended on by active/done successor %q", cardID, succ)
		}
	}
	target := lastCol - 1
	// Reopen 不接受 expedite 参数：普通容量判定。
	if lim := b.columns[target].wipLimit; lim != LimitUnlimited && b.colCount[target]+1 > lim {
		return nil, newError(ErrColumnFull, "column %d is full (limit %d, occupancy %d)", target, lim, b.colCount[target])
	}
	if b.ownerCount[c.owner]+1 > b.ownerLimit {
		return nil, newError(ErrOwnerFull, "owner %q is at WIP limit %d", c.owner, b.ownerLimit)
	}
	b.colCount[lastCol]--
	b.colCount[target]++
	b.ownerCount[c.owner]++
	c.col = target
	c.version++
	b.lastNow = now
	return b.snapshot(c), nil
}

// ChangeOwner 修改负责人。若卡片在进行中列，新负责人占用数须不超过 G。
func (b *Board) ChangeOwner(user, cardID, newOwner string, expectVer int, now int64) (*Card, error) {
	if newOwner == "" {
		return nil, newError(ErrInvalidArgument, "new owner is empty")
	}
	c, err := b.resolveOp(cardID, expectVer, now)
	if err != nil {
		return nil, err
	}
	if newOwner == c.owner {
		return nil, newError(ErrIllegalFlow, "owner unchanged: %q", newOwner)
	}
	// 加急卡片同样占用负责人名额；加急仅在 Move 移入时豁免，改负责人不豁免。
	if b.isInProgress(c.col) && b.ownerCount[newOwner]+1 > b.ownerLimit {
		return nil, newError(ErrOwnerFull, "new owner %q would exceed WIP limit %d", newOwner, b.ownerLimit)
	}
	if b.isInProgress(c.col) {
		b.ownerCount[c.owner]--
		b.ownerCount[newOwner]++
	}
	c.owner = newOwner
	c.version++
	b.lastNow = now
	return b.snapshot(c), nil
}
