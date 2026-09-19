package ontology

import "crypto/rand"

// Scan 返回一页对象、下一页游标以及是否还有更多。
//
// cursor 为 "" 时开启一次新的遍历（以当前集合为快照）；之后使用上一页
// 返回的 NextCursor 继续。语义见 README 与各测试：
//   - limit < 0：返回 ErrInvalidLimit；
//   - limit == 0：返回空页、游标不前进、不报错；
//   - limit 大于/等于剩余元素数：返回剩余全部且 HasMore 为 false；
//   - 末尾之后继续 Scan：空页、HasMore 为 false、不报错。
//
// 同一游标可被任意并发重复 Scan：结果完全一致（幂等），游标不会重复推进。
func (st *Store) Scan(cursor string, limit int) (Page, error) {
	if limit < 0 {
		return Page{}, ErrInvalidLimit
	}

	var sess *session
	var position int64
	if cursor == "" {
		id := make([]byte, cursorIDLen)
		if _, err := rand.Read(id); err != nil {
			panic(err)
		}
		sess = newSession(id, st.secret, st.snapshotKeys())
		st.registerSession(sess)
	} else {
		id, pos, err := decodeCursor(st.secret, cursor)
		if err != nil {
			return Page{}, err
		}
		sess = st.lookupSession(id)
		if sess == nil {
			return Page{}, ErrCursorInvalidated
		}
		position = pos
	}

	// limit == 0：不前进、不计算、不报错，原样返回当前位置的游标。
	if limit == 0 {
		return Page{
			Items:      []Item{},
			NextCursor: encodeCursor(sess.secret, sess.id, position),
			HasMore:    false,
			Changes:    sess.changeInfo(),
		}, nil
	}

	key := pageKey{position: position, limit: limit}
	sess.mu.Lock()
	result := sess.cache[key]
	if result == nil {
		result = st.computePage(sess, position, limit)
		sess.position = result.nextPos
		sess.truncated = result.truncated
		sess.cache[key] = result
	}
	sess.mu.Unlock()

	return Page{
		Items:      result.items,
		NextCursor: encodeCursor(sess.secret, sess.id, result.nextPos),
		HasMore:    result.hasMore,
		Dropped:    result.dropped,
		truncated:  result.truncated,
		Changes:    sess.changeInfo(),
	}, nil
}

// computePage 必须在持有 sess.mu 时调用；内部只短暂获取读锁拷贝数据。
func (st *Store) computePage(sess *session, position int64, limit int) *pageResult {
	st.mu.RLock()
	result := &pageResult{items: make([]Item, 0, limit)}
	index := position
	for ; index < int64(len(sess.snapshot)) && len(result.items) < limit; index++ {
		key := sess.snapshot[index]
		if value, ok := st.items[key]; ok {
			result.items = append(result.items, Item{Key: key, Value: value})
		}
	}
	remaining := int64(len(sess.snapshot)) - index
	st.mu.RUnlock()

	// 从旧位置到结束位置之间，凡快照中存在、但当前已不存在的元素都是“丢弃”。
	result.nextPos = index
	result.dropped = int(index-position) - len(result.items)

	if remaining > 0 {
		// 因 limit 在快照中段停止：截断。剩余数含未来可能被丢弃的元素。
		result.hasMore = true
		result.truncated = int(remaining)
	} else {
		// 已到快照末尾：即使本页全部因删除而丢弃，也不是截断。
		result.hasMore = false
		result.truncated = 0
	}
	return result
}
