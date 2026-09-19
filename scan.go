package ontology

// Scan 按游标遍历一页。
//
// cursor 为空：开启新的遍历会话（快照语义）。
// cursor 非空：在其所属会话中从确切续点继续。游标是纯位置票据，
// 不携带"已推进"状态，因此同一游标可被幂等、并发地重复 Scan。
//
// limit == 0：返回空页，游标不前进，不报错。
// limit < 0：返回 ErrInvalidLimit（与空页可区分）。
// limit 大于或恰好等于剩余元素数：返回剩余全部且 HasMore=false。
func (s *Store) Scan(cursor string, limit int) (Page, error) {
	if limit < 0 {
		return Page{}, ErrInvalidLimit
	}

	var sn *session
	sessionID := ""
	start := 0
	if cursor == "" {
		keys, _ := s.snapshot()
		sn = newSession(keys)
		id, err := s.registerSession(sn)
		if err != nil {
			return Page{}, err
		}
		sessionID = id
	} else {
		id, frontier, err := s.decodeCursor(cursor)
		if err != nil {
			return Page{}, err
		}
		var ok bool
		sn, ok = s.lookupSession(id)
		if !ok {
			return Page{}, ErrSessionInvalid
		}
		sessionID = id
		start = frontier
	}

	// limit 为 0：空页且游标不前进（新会话也不创建游标）。
	if limit == 0 {
		sn.mu.Lock()
		changes := sn.changeKind()
		discarded := sn.discard
		sn.mu.Unlock()
		return Page{Changes: changes, Discarded: discarded, sessionID: sessionID}, nil
	}

	sn.mu.Lock()
	keys := append([]string(nil), sn.keys...)
	sn.mu.Unlock()

	if start > len(keys) {
		start = len(keys)
	}

	live := s.liveValues() // 短锁拷贝，不在锁上做遍历

	objs := make([]Object, 0, limit)
	discarded := 0
	consumed := start
	for consumed < len(keys) && len(objs) < limit {
		key := keys[consumed]
		value, alive := live[key]
		if !alive {
			discarded++ // 丢弃：快照中存在但已被删除，不属于截断
		} else {
			objs = append(objs, Object{Key: key, Value: value})
		}
		consumed++
	}

	hasMore := consumed < len(keys)
	nextCursor := ""
	if hasMore {
		tok, err := s.encodeCursor(sessionID, consumed)
		if err != nil {
			return Page{}, err
		}
		nextCursor = tok
	}

	// 仅当续点越过会话已确认位置时推进统计，保证同游标并发/重复不重复计数。
	sn.mu.Lock()
	if consumed > sn.frontier {
		sn.frontier = consumed
		sn.discard += discarded
		sn.returned += len(objs)
	}
	changes := sn.changeKind()
	sn.mu.Unlock()

	return Page{
		Objects:   objs,
		Cursor:    nextCursor,
		HasMore:   hasMore,
		Truncated: hasMore,
		Discarded: discarded,
		Changes:   changes,
		sessionID: sessionID,
	}, nil
}
