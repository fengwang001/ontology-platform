package kanban

// AddDep 声明 cardID 的前置 prerequisiteID（有向边 prerequisiteID -> cardID）。
func (b *Board) AddDep(user, cardID, prerequisiteID string, expectVer int, now int64) error {
	if cardID == "" || prerequisiteID == "" {
		return newError(ErrInvalidArgument, "card id and prerequisite id must be non-empty")
	}
	c, err := b.resolveOp(cardID, expectVer, now)
	if err != nil {
		return err
	}
	pc, ok := b.cards[prerequisiteID]
	if !ok {
		return newError(ErrCardNotFound, "prerequisite card %q does not exist", prerequisiteID)
	}
	if cardID == prerequisiteID {
		return newError(ErrDependency, "self dependency is not allowed: %q", cardID)
	}
	if _, dup := c.prereqs[prerequisiteID]; dup {
		return newError(ErrDependency, "dependency %q -> %q already exists", prerequisiteID, cardID)
	}
	lastCol := len(b.columns) - 1
	// 已离开待办的卡片，不得新增尚未完成的前置。
	if c.col != 0 && pc.col != lastCol {
		return newError(ErrDependency, "prerequisite %q is not done; active card %q cannot take it", prerequisiteID, cardID)
	}
	// 成环判断（允许与图大小有关）：沿 cardID 的后继方向 DFS，
	// 若能到达 prerequisiteID，则新边 prerequisiteID -> cardID 成环。
	if b.reaches(cardID, prerequisiteID) {
		return newError(ErrDependency, "adding %q -> %q creates a cycle", prerequisiteID, cardID)
	}

	c.prereqs[prerequisiteID] = struct{}{}
	if b.successors[prerequisiteID] == nil {
		b.successors[prerequisiteID] = map[string]struct{}{}
	}
	b.successors[prerequisiteID][cardID] = struct{}{}
	c.version++
	b.lastNow = now
	return nil
}

// RemoveDep 删除一条前置声明；不存在报依赖错误。
func (b *Board) RemoveDep(user, cardID, prerequisiteID string, expectVer int, now int64) error {
	if cardID == "" || prerequisiteID == "" {
		return newError(ErrInvalidArgument, "card id and prerequisite id must be non-empty")
	}
	c, err := b.resolveOp(cardID, expectVer, now)
	if err != nil {
		return err
	}
	if _, ok := b.cards[prerequisiteID]; !ok {
		return newError(ErrCardNotFound, "prerequisite card %q does not exist", prerequisiteID)
	}
	if _, ok := c.prereqs[prerequisiteID]; !ok {
		return newError(ErrDependency, "dependency %q -> %q does not exist", prerequisiteID, cardID)
	}
	delete(c.prereqs, prerequisiteID)
	if succ := b.successors[prerequisiteID]; succ != nil {
		delete(succ, cardID)
		if len(succ) == 0 {
			delete(b.successors, prerequisiteID)
		}
	}
	c.version++
	b.lastNow = now
	return nil
}

// reaches 判断从 from 沿后继边是否能到达 target（DFS，迭代避免深递归）。
func (b *Board) reaches(from, target string) bool {
	visited := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for succ := range b.successors[n] {
			if succ == target {
				return true
			}
			if !visited[succ] {
				visited[succ] = true
				stack = append(stack, succ)
			}
		}
	}
	return false
}
