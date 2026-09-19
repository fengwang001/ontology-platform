package ontology

// Stats 报告一个游标所属遍历会话的累计跳过情况与变更标记。
//
// 返回的 SkipStats 按原因分类：
//   - Truncated：最近一次实际取页时因 limit 截断、留在游标后的快照元素数；
//   - Deleted：因遍历期间被删除而丢弃的快照元素数（累计）；
//   - Inserted：遍历期间新增、按快照语义不会出现的元素数（累计）。
//
// 游标格式非法返回 ErrCursorMalformed；会话已失效返回 ErrCursorInvalidated。
func (st *Store) Stats(cursor string) (SkipStats, ChangeInfo, error) {
	id, pos, err := decodeCursor(st.secret, cursor)
	if err != nil {
		return SkipStats{}, ChangeInfo{}, err
	}
	_ = pos
	sess := st.lookupSession(id)
	if sess == nil {
		return SkipStats{}, ChangeInfo{}, ErrCursorInvalidated
	}
	sess.mu.Lock()
	truncated := sess.truncated
	sess.mu.Unlock()
	return sess.skipStats(truncated), sess.changeInfo(), nil
}

// Invalidate 显式失效一个遍历会话。失效后该会话的任何游标再用于
// Scan 或 Stats 都返回 ErrCursorInvalidated（区别于 ErrCursorMalformed）。
func (st *Store) Invalidate(cursor string) error {
	id, _, err := decodeCursor(st.secret, cursor)
	if err != nil {
		return err
	}
	var kid [cursorIDLen]byte
	copy(kid[:], id)
	st.mu.Lock()
	_, ok := st.sessions[kid]
	if ok {
		delete(st.sessions, kid)
	}
	st.mu.Unlock()
	if !ok {
		return ErrCursorInvalidated
	}
	return nil
}
