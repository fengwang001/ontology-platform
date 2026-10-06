package whiteboard

import "slices"

// Add 在最顶放入新元素。
func (b *Board) Add(user, id string, now int64) error {
	if !validUser(user) || !validID(id) || !validTime(now) {
		return reject(KindInvalidArgument, "Add: user/id must be non-empty and now in [0,1e12]")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if now < b.lastTs {
		return reject(KindClockBackward, "Add: now=%d < last=%d", now, b.lastTs)
	}
	if _, exists := b.elems[id]; exists {
		return reject(KindIllegal, "Add: id %q already exists (elements and groups share one namespace)", id)
	}
	if _, exists := b.groups[id]; exists {
		return reject(KindIllegal, "Add: id %q is an existing group", id)
	}
	// Add 无被触碰的既有元素，因此不会被锁阻止。
	b.rev++
	x := newTreapNode(id, b.rng.Uint64())
	b.order.pushBack(x)
	b.nodes[id] = x
	b.elems[id] = &elem{id: id, lastRev: b.rev}
	b.lastTs = now
	return nil
}

// Group 建立组合：成员须全部是尚未属于任何组合的元素，数量 2..200；
// 不改变元素次序；影响全部成员的 lastRev。
func (b *Board) Group(user, groupID string, ids []string, now int64) error {
	if !validUser(user) || !validID(groupID) || !validTime(now) {
		return reject(KindInvalidArgument, "Group: bad user/groupID/now")
	}
	if len(ids) < MinGroupMembers || len(ids) > MaxGroupMembers {
		return reject(KindInvalidArgument, "Group: member count %d out of [%d,%d]", len(ids), MinGroupMembers, MaxGroupMembers)
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if !validID(id) {
			return reject(KindInvalidArgument, "Group: empty member id")
		}
		if _, dup := seen[id]; dup {
			return reject(KindInvalidArgument, "Group: duplicate member %q", id)
		}
		seen[id] = struct{}{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if now < b.lastTs {
		return reject(KindClockBackward, "Group: now=%d < last=%d", now, b.lastTs)
	}
	if _, exists := b.groups[groupID]; exists {
		return reject(KindIllegal, "Group: group id %q already exists", groupID)
	}
	if _, exists := b.elems[groupID]; exists {
		return reject(KindIllegal, "Group: group id %q collides with element", groupID)
	}
	for _, id := range ids {
		if _, ok := b.elems[id]; !ok {
			return reject(KindNotFound, "Group: member %q does not exist", id)
		}
	}
	for _, id := range ids {
		if b.elems[id].group != "" {
			return reject(KindIllegal, "Group: member %q already belongs to group %q", id, b.elems[id].group)
		}
	}
	for _, id := range ids {
		if lerr := b.touchedLock(id, user, now); lerr != nil {
			return lerr
		}
	}
	members := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		members[id] = struct{}{}
	}
	b.groups[groupID] = members
	b.rev++
	for _, id := range ids {
		b.elems[id].group = groupID
		b.elems[id].lastRev = b.rev
	}
	b.lastTs = now
	return nil
}

// Ungroup 解散组合：不改变次序，影响全部成员的 lastRev。
func (b *Board) Ungroup(user, groupID string, now int64) error {
	if !validUser(user) || !validID(groupID) || !validTime(now) {
		return reject(KindInvalidArgument, "Ungroup: bad user/groupID/now")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if now < b.lastTs {
		return reject(KindClockBackward, "Ungroup: now=%d < last=%d", now, b.lastTs)
	}
	members, ok := b.groups[groupID]
	if !ok {
		if _, isElem := b.elems[groupID]; isElem {
			return reject(KindIllegal, "Ungroup: %q is an element, not a group", groupID)
		}
		return reject(KindNotFound, "Ungroup: group %q does not exist", groupID)
	}
	for id := range members {
		if lerr := b.touchedLock(id, user, now); lerr != nil {
			return lerr
		}
	}
	b.rev++
	for id := range members {
		b.elems[id].group = ""
		b.elems[id].lastRev = b.rev
	}
	delete(b.groups, groupID)
	b.lastTs = now
	return nil
}

// Remove 删除元素或组合：删除组合时删除其全部成员；被整体删除的元素都算受影响。
func (b *Board) Remove(user, id string, now int64) error {
	if !validUser(user) || !validID(id) || !validTime(now) {
		return reject(KindInvalidArgument, "Remove: bad user/id/now")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if now < b.lastTs {
		return reject(KindClockBackward, "Remove: now=%d < last=%d", now, b.lastTs)
	}
	var victims []string
	if members, ok := b.groups[id]; ok {
		for m := range members {
			victims = append(victims, m)
		}
	} else if _, ok := b.elems[id]; ok {
		victims = []string{id}
	} else {
		return reject(KindNotFound, "Remove: %q does not exist", id)
	}
	// 组合成员只能整体随组合移除：单个元素若属于组合，直接 Remove 属于非法目标。
	if len(victims) == 1 && b.elems[id].group != "" {
		return reject(KindIllegal, "Remove: element %q belongs to group %q; remove the group or ungroup first", id, b.elems[id].group)
	}
	// 按当前自底向上次序排序，便于确定性处理。
	slices.SortFunc(victims, func(x, y string) int {
		return b.order.rankOf(b.nodes[x]) - b.order.rankOf(b.nodes[y])
	})
	for _, vid := range victims {
		if lerr := b.touchedLock(vid, user, now); lerr != nil {
			return lerr
		}
	}
	b.rev++
	for _, vid := range victims {
		b.order.erase(b.nodes[vid])
		delete(b.nodes, vid)
		delete(b.elems, vid)
		delete(b.locks, vid)
	}
	if _, isGroup := b.groups[id]; isGroup {
		delete(b.groups, id)
		delete(b.locks, id)
	}
	b.lastTs = now
	return nil
}

// Reorder 把 target（元素或组合）移动到 anchor 的上/下方并紧贴。
func (b *Board) Reorder(user, target, anchor string, side Side, baseRev, now int64) error {
	if !validUser(user) || !validID(target) || !validID(anchor) || !validTime(now) {
		return reject(KindInvalidArgument, "Reorder: bad user/target/anchor/now")
	}
	if side != Above && side != Below {
		return reject(KindInvalidArgument, "Reorder: side must be Above or Below")
	}
	if baseRev < 0 {
		return reject(KindInvalidArgument, "Reorder: baseRev must be >= 0")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if now < b.lastTs {
		return reject(KindClockBackward, "Reorder: now=%d < last=%d", now, b.lastTs)
	}
	if _, ok := b.elems[anchor]; !ok {
		return reject(KindNotFound, "Reorder: anchor %q does not exist", anchor)
	}
	var moving []string
	if members, ok := b.groups[target]; ok {
		for m := range members {
			moving = append(moving, m)
		}
	} else if e, ok := b.elems[target]; ok {
		if e.group != "" {
			return reject(KindIllegal, "Reorder: element %q belongs to group %q; move the group instead", target, e.group)
		}
		moving = []string{target}
	} else {
		return reject(KindNotFound, "Reorder: target %q does not exist", target)
	}
	inMoving := make(map[string]struct{}, len(moving))
	for _, m := range moving {
		inMoving[m] = struct{}{}
	}
	if _, ok := inMoving[anchor]; ok {
		return reject(KindIllegal, "Reorder: anchor %q is inside the moving set", anchor)
	}
	for _, m := range moving {
		if lerr := b.touchedLock(m, user, now); lerr != nil {
			return lerr
		}
	}
	// 版本冲突：移动集合中任一元素的最近影响修订号大于 baseRev。
	for _, m := range moving {
		if b.elems[m].lastRev > baseRev {
			return reject(KindConflict, "Reorder: element %q lastRev=%d > baseRev=%d", m, b.elems[m].lastRev, baseRev)
		}
	}
	// 收集移动段：按当前自底向上次序（曾被其他元素隔开也照样收集），段内相对次序保持。
	slices.SortFunc(moving, func(x, y string) int {
		return b.order.rankOf(b.nodes[x]) - b.order.rankOf(b.nodes[y])
	})
	anchorRank := b.order.rankOf(b.nodes[anchor])
	// 计算删除移动元素后 anchor 的新名次。
	anchorRankAfter := anchorRank
	for _, m := range moving {
		if b.order.rankOf(b.nodes[m]) < anchorRank {
			anchorRankAfter--
		}
	}
	// 摘除移动元素并保持其相对次序。
	detached := make([]*node, 0, len(moving))
	for _, m := range moving {
		detached = append(detached, b.order.erase(b.nodes[m]))
	}
	// 重新插入：Below 时从紧邻 anchor 下方依次向下放，Above 时从紧邻 anchor 上方向上放。
	b.rev++
	if side == Below {
		insertPos := anchorRankAfter - 1 // 0 基位置：anchor 当前名次之前
		for i, x := range detached {
			b.order.insertAt(insertPos+i, x)
		}
	} else {
		insertPos := anchorRankAfter // 0 基位置：anchor 当前名次之后的第一个位置
		for _, x := range detached {
			b.order.insertAt(insertPos, x)
			insertPos++
		}
	}
	for _, m := range moving {
		b.elems[m].lastRev = b.rev
	}
	b.lastTs = now
	return nil
}
