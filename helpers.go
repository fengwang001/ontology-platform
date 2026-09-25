package ontology

// heapPush 把元素同时放入堆与索引/map。调用方必须持有写锁。
func (t *TopK) heapPush(e Element) {
	t.hp.push(e)
	t.items[e.ID] = e
	t.reindex()
}

// heapRemove 删除堆上指定位置，并同步索引/map。调用方必须持有写锁。
func (t *TopK) heapRemove(pos int) {
	old := t.hp.remove(pos)
	delete(t.index, old.ID)
	delete(t.items, old.ID)
	t.reindex()
}

// reindex 用当前堆内容重建 ID->位置 映射。
// 仅在单次写操作内调用，元素数不超过 K。
func (t *TopK) reindex() {
	for i, e := range t.hp.items {
		t.index[e.ID] = i
	}
}

// replace 处理已存在 ID 的分数覆盖：
// 以"去掉旧元素后剩余集合中的最差元素"为截断阈值，
// 覆盖后的新分数若严格优于阈值（并列时 ID 更小）则保留，
// 否则该 ID 立即掉出 Top-K。
func (t *TopK) replace(pos int, incoming Element) {
	old := t.hp.items[pos]
	if len(t.hp.items) == 1 {
		t.hp.items[0] = incoming
		t.items[incoming.ID] = incoming
		return
	}

	threshold := -1
	for i := range t.hp.items {
		if i == pos {
			continue
		}
		// 阈值 = 除旧元素外排名最靠后的元素（并列时 ID 更大者更靠后）。
		if threshold == -1 || rankLess(t.dir, t.hp.items[threshold], t.hp.items[i]) {
			threshold = i
		}
	}

	if rankLess(t.dir, incoming, t.hp.items[threshold]) {
		t.hp.items[pos] = incoming
		t.hp.fix(pos)
		t.items[incoming.ID] = incoming
		t.reindex()
		return
	}
	t.hp.remove(pos)
	delete(t.index, old.ID)
	delete(t.items, old.ID)
	t.reindex()
}
