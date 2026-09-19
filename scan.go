package ontology

// Scan returns one page of a snapshot traversal.
//
// An empty cursor begins a brand-new traversal; otherwise the cursor must be
// one returned by a previous Scan of this store. Scan is idempotent: using
// the same cursor again yields the same page and never advances twice, even
// under concurrent calls.
func (s *Store) Scan(cursor string, limit int) (*Page, error) {
	if limit < 0 {
		return nil, ErrInvalidLimit
	}

	var sess *Session
	var index int
	if cursor == "" {
		sess = s.BeginTraversal()
	} else {
		var err error
		sess, index, err = s.decodeCursor(cursor)
		if err != nil {
			return nil, err
		}
	}

	if limit == 0 {
		return sess.zeroLimitPage(index)
	}
	return sess.pageAt(index, limit)
}

// zeroLimitPage returns an empty page and leaves the cursor exactly where it
// was: the traversal does not advance and statistics are not consumed.
func (sess *Session) zeroLimitPage(index int) (*Page, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if index < 0 || index > len(sess.keys) {
		return nil, ErrInvalidCursor
	}
	return &Page{
		Items:     []Object{},
		Next:      sess.store.encodeCursor(sess.id, index),
		HasMore:   index < len(sess.keys),
		Changed:   sess.changed,
		Truncated: false,
		Discarded: 0,
	}, nil
}

func (sess *Session) pageAt(index, limit int) (*Page, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if index < 0 || index > len(sess.keys) {
		return nil, ErrInvalidCursor
	}

	key := pageCacheKey{index: index, limit: limit}
	if cached, ok := sess.cache[key]; ok {
		return clonePage(cached), nil
	}

	sess.countHiddenAt(index)

	taken := 0
	end := index
	var liveKeys []string
	// Walk the snapshot; live keys fill the page, deleted snapshot keys are
	// discarded (never confused with truncation).
	for end < len(sess.keys) && taken < limit {
		k := sess.keys[end]
		if _, ok := sess.alive[k]; ok {
			liveKeys = append(liveKeys, k)
			taken++
		}
		end++
	}

	// A key could be deleted between the alive-check above and lookup;
	// resolve rechecks under one combined lock acquisition.
	discarded := end - index - len(liveKeys)
	items := sess.resolve(liveKeys, &discarded)
	sess.discarded += discarded
	page := &Page{
		Items:     items,
		Changed:   sess.changed,
		Discarded: discarded,
	}
	if end < len(sess.keys) {
		page.Next = sess.store.encodeCursor(sess.id, end)
		page.HasMore = true
		page.Truncated = len(items) == limit
	} else {
		page.Next = sess.store.encodeCursor(sess.id, len(sess.keys))
		page.HasMore = false
		page.Truncated = false
	}
	sess.cache[key] = page
	return clonePage(page), nil
}

// countHiddenAt counts previously-uncounted insertions sorted at or before
// the current frontier; those rows can never appear in this traversal.
func (sess *Session) countHiddenAt(index int) {
	var boundary string
	if index < len(sess.keys) {
		boundary = sess.keys[index]
	}
	for k := range sess.inserted {
		if index == len(sess.keys) || k <= boundary {
			sess.hidden++
			delete(sess.inserted, k)
		}
	}
}

// resolve fetches live values while holding the session lock, taking the
// store read lock only briefly (session-lock-then-store-lock is the single
// lock order used everywhere). Keys deleted in the tiny race window count as
// discarded rather than vanishing silently.
func (sess *Session) resolve(keys []string, discarded *int) []Object {
	s := sess.store
	s.mu.RLock()
	out := make([]Object, 0, len(keys))
	for _, k := range keys {
		v, ok := s.data[k]
		if !ok {
			*discarded++
			continue
		}
		out = append(out, Object{Key: k, Value: v})
	}
	s.mu.RUnlock()
	return out
}

func clonePage(p *Page) *Page {
	items := make([]Object, len(p.Items))
	copy(items, p.Items)
	return &Page{
		Items:     items,
		Next:      p.Next,
		HasMore:   p.HasMore,
		Changed:   p.Changed,
		Truncated: p.Truncated,
		Discarded: p.Discarded,
	}
}
