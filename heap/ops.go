package heap

// Insert 把行 (key, xmin=xid, xmax=0) 放进有空槽的编号最小页的最小空槽，
// 在索引中登记 (key, 页, 槽)，并清除该页全可见位。
func (t *Table) Insert(key string, xid int64) (page, slot int, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if xid <= 0 {
		return 0, 0, ErrInvalidXid
	}
	page, slot = -1, -1
	for p := range t.pages {
		for s := range t.pages[p] {
			if t.pages[p][s] == nil {
				page, slot = p, s
				break
			}
		}
		if page >= 0 {
			break
		}
	}
	if page < 0 {
		return 0, 0, ErrNoFreeSlot
	}
	t.pages[page][slot] = &Row{Key: key, Xmin: xid}
	t.indexInsert(Entry{Key: key, Page: page, Slot: slot})
	t.allVis[page] = false
	return page, slot, nil
}

// Delete 给 (页, 槽) 处的行写入 xmax=xid，并清除该页全可见位。
func (t *Table) Delete(page, slot int, xid int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if xid <= 0 {
		return ErrInvalidXid
	}
	if page < 0 || page >= len(t.pages) || slot < 0 || slot >= len(t.pages[page]) {
		return ErrSlotOutOfRange
	}
	row := t.pages[page][slot]
	if row == nil {
		return ErrSlotEmpty
	}
	if row.Xmax != 0 {
		return ErrAlreadyDeleted
	}
	row.Xmax = xid
	t.allVis[page] = false
	return nil
}

// Snapshot 登记快照值 s 并返回快照编号（从 1 起连续递增，被拒绝的调用不占号）。
func (t *Table) Snapshot(s int64) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if s <= 0 {
		return 0, ErrInvalidSnapshotValue
	}
	if s < t.maxH {
		return 0, ErrSnapshotTooOld
	}
	id := t.nextSnapID
	t.nextSnapID++
	t.snapshots[id] = s
	return id, nil
}

// Release 注销指定编号的快照。
func (t *Table) Release(id int) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.snapshots[id]; !ok {
		return ErrUnknownSnapshot
	}
	delete(t.snapshots, id)
	return nil
}

// Vacuum 先物理移除该页中 xmax!=0 且 xmax<h 的行（连同其索引条目，槽位变空），
// 再令该页全可见位 = 剩余每一行都满足 xmin<h 且 xmax=0（空页为真）；
// 成功后更新全局最大已用 h。
func (t *Table) Vacuum(page int, h int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if h <= 0 {
		return ErrInvalidHorizon
	}
	if page < 0 || page >= len(t.pages) {
		return ErrPageOutOfRange
	}
	for _, s := range t.snapshots {
		if s < h {
			return ErrSnapshotBlocking
		}
	}

	for slot, row := range t.pages[page] {
		if row != nil && row.Xmax != 0 && row.Xmax < h {
			t.indexRemove(Entry{Key: row.Key, Page: page, Slot: slot})
			t.pages[page][slot] = nil
		}
	}

	allVis := true
	for _, row := range t.pages[page] {
		if row != nil && !(row.Xmin < h && row.Xmax == 0) {
			allVis = false
			break
		}
	}
	t.allVis[page] = allVis
	if h > t.maxH {
		t.maxH = h
	}
	return nil
}

// Scan 按 (key, 页, 槽) 升序遍历索引中 lo<=key<hi 的条目：
// 条目所在页全可见位为真时直接产出该行且不回表；
// 否则回表计一次，仅当该行 xmin<s 且 (xmax=0 或 xmax>=s) 时产出。
func (t *Table) Scan(lo, hi string, snapID int) (ScanResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	var res ScanResult
	if lo > hi {
		return res, ErrInvalidRange
	}
	s, ok := t.snapshots[snapID]
	if !ok {
		return res, ErrUnknownSnapshot
	}

	for _, e := range t.index {
		if e.Key < lo {
			continue
		}
		if e.Key >= hi {
			break
		}
		if t.allVis[e.Page] {
			res.Rows = append(res.Rows, e)
			res.Skips++
			continue
		}
		res.Fetches++
		row := t.pages[e.Page][e.Slot]
		if row != nil && row.Xmin < s && (row.Xmax == 0 || row.Xmax >= s) {
			res.Rows = append(res.Rows, e)
		}
	}
	return res, nil
}
