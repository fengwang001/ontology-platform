package ontology

// Page is one page of a traversal.
type Page struct {
	// Items are the live objects of this page, in primary-key order.
	Items []Object
	// Next is the opaque cursor for the following page. When the page was
	// truncated by limit, Next points at the exact continuation position.
	Next string
	// HasMore reports whether any live element remains after this page.
	// A page truncated by limit always has HasMore == true; a page that
	// reaches the end of the remaining elements always has HasMore == false.
	HasMore bool
	// Dropped counts snapshot elements that were deleted before this page
	// was produced. Dropped elements are discarded, not truncated: they do
	// not appear in any page and are not reachable via Next.
	Dropped int
}

// Scan returns one page of a traversal in primary-key order.
//
// An empty cursor starts a new traversal session whose snapshot is taken at
// this call; every other cursor must be one returned by a previous Scan.
//
// limit semantics:
//   - limit < 0: argument error (ErrNegativeLimit), no page is produced.
//   - limit == 0: empty page, cursor does not advance, no error.
//   - limit >= remaining elements: all remaining elements, HasMore == false.
//
// Scan is idempotent: scanning the same cursor with the same limit yields
// the same page, even concurrently.
func (s *Store) Scan(cursor string, limit int) (Page, error) {
	if limit < 0 {
		return Page{}, ErrNegativeLimit
	}
	var (
		sess *session
		pos  int
	)
	if cursor == "" {
		sess = s.newSession()
	} else {
		id, p, err := s.decodeCursor(cursor)
		if err != nil {
			return Page{}, err
		}
		ok := false
		sess, ok = s.findSession(id)
		if !ok {
			return Page{}, ErrInvalidSession
		}
		if p < 0 || p > len(sess.keys) {
			return Page{}, ErrInvalidCursor
		}
		pos = p
	}

	items := make([]Object, 0, limit)
	dropped := 0
	next := pos

	s.mu.RLock()
	for next < len(sess.keys) && len(items) < limit {
		key := sess.keys[next]
		if v, ok := s.values[key]; ok {
			items = append(items, Object{Key: key, Value: v})
		} else {
			dropped++
		}
		next++
	}
	hasMore := false
	for i := next; i < len(sess.keys); i++ {
		if _, ok := s.values[sess.keys[i]]; ok {
			hasMore = true
			break
		}
	}
	s.mu.RUnlock()

	sess.advanceHWM(next)
	return Page{
		Items:   items,
		Next:    s.encodeCursor(sess.id, next),
		HasMore: hasMore,
		Dropped: dropped,
	}, nil
}
