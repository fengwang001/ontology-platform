package chat

// Send appends a message to the channel and returns its sequence number.
// Accepted sends take the next dense seq starting from 1; rejected sends do
// not consume a seq. Cost is O(M log H) where M is the mention count and H
// the history length; it does not depend on the member count.
func (c *Channel) Send(user, body string, mentions []string, now int64) (int, error) {
	const op = "Send"
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateUser(op, user); err != nil {
		return 0, err
	}
	if err := validateBody(op, body); err != nil {
		return 0, err
	}
	if err := validateNow(op, now); err != nil {
		return 0, err
	}
	if err := c.checkMentionShape(op, mentions); err != nil {
		return 0, err
	}
	for _, id := range mentions {
		if id == user {
			return 0, reject(op, ErrInvalidParam, "sender %q cannot mention itself", user)
		}
	}
	if err := c.checkClock(op, now); err != nil {
		return 0, err
	}
	if _, err := c.requireMember(op, user); err != nil {
		return 0, err
	}

	seq := len(c.messages) + 1
	set := sortedUnique(mentions)
	c.messages = append(c.messages, message{
		author:   user,
		body:     body,
		mentions: set,
		sentAt:   now,
	})
	c.ownSet(user).insert(seq)
	for _, id := range set {
		c.mentionSet(id).insert(seq)
	}
	c.lastNow = now
	return seq, nil
}

// Edit replaces the body and the whole mention set of a message. Only the
// author may edit, only while now-sentAt < E, at most K times, and never on
// a recalled message.
func (c *Channel) Edit(user string, seq int, body string, mentions []string, now int64) error {
	const op = "Edit"
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateUser(op, user); err != nil {
		return err
	}
	if err := validateBody(op, body); err != nil {
		return err
	}
	if err := validateNow(op, now); err != nil {
		return err
	}
	if seq < 1 {
		return reject(op, ErrInvalidParam, "seq %d < 1", seq)
	}
	if err := c.checkMentionShape(op, mentions); err != nil {
		return err
	}
	if err := c.checkClock(op, now); err != nil {
		return err
	}
	if _, err := c.requireMember(op, user); err != nil {
		return err
	}
	if seq > len(c.messages) {
		return reject(op, ErrMessageNotFound, "seq %d > latest %d", seq, len(c.messages))
	}
	msg := &c.messages[seq-1]
	for _, id := range mentions {
		if id == msg.author {
			return reject(op, ErrInvalidParam, "author %q cannot be mentioned", id)
		}
	}
	if user != msg.author {
		return reject(op, ErrPermissionDenied, "%q is not the author of #%d", user, seq)
	}
	if msg.recalled {
		return reject(op, ErrAlreadyRecalled, "message #%d is recalled", seq)
	}
	if now-msg.sentAt >= c.editWindow {
		return reject(op, ErrTimeout, "edit window %ds elapsed", c.editWindow)
	}
	if msg.edits >= c.maxEdits {
		return reject(op, ErrEditLimit, "message #%d already edited %d times", seq, msg.edits)
	}

	next := sortedUnique(mentions)
	for _, id := range msg.mentions {
		if !containsString(next, id) {
			c.mentionSet(id).remove(seq)
		}
	}
	for _, id := range next {
		if !containsString(msg.mentions, id) {
			c.mentionSet(id).insert(seq)
		}
	}
	msg.body = body
	msg.mentions = next
	msg.edits++
	c.lastNow = now
	return nil
}

// Recall turns a message into a placeholder that keeps its seq. The author
// may recall while now-sentAt < R; an admin may recall any message at any
// time. Recalling clears body and mentions and never reuses the seq.
func (c *Channel) Recall(user string, seq int, now int64) error {
	const op = "Recall"
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateUser(op, user); err != nil {
		return err
	}
	if err := validateNow(op, now); err != nil {
		return err
	}
	if seq < 1 {
		return reject(op, ErrInvalidParam, "seq %d < 1", seq)
	}
	if err := c.checkClock(op, now); err != nil {
		return err
	}
	caller, err := c.requireMember(op, user)
	if err != nil {
		return err
	}
	if seq > len(c.messages) {
		return reject(op, ErrMessageNotFound, "seq %d > latest %d", seq, len(c.messages))
	}
	msg := &c.messages[seq-1]
	if user != msg.author && !caller.admin {
		return reject(op, ErrPermissionDenied, "%q is neither author nor admin", user)
	}
	if msg.recalled {
		return reject(op, ErrAlreadyRecalled, "message #%d is recalled", seq)
	}
	if !caller.admin && now-msg.sentAt >= c.recallWindow {
		return reject(op, ErrTimeout, "recall window %ds elapsed", c.recallWindow)
	}

	msg.recalled = true
	msg.recalledBy = user
	msg.recalledAt = now
	for _, id := range msg.mentions {
		c.mentionSet(id).remove(seq)
	}
	msg.mentions = nil
	msg.body = ""
	c.ownSet(msg.author).remove(seq)
	c.recalledSeqs.insert(seq)
	c.lastNow = now
	return nil
}

// MarkRead advances the user's read watermark to upto. upto equal to the
// current watermark is an accepted no-op; below it is a rollback rejection;
// above the latest seq is an out-of-range rejection. The watermark never
// decreases while the user stays joined.
func (c *Channel) MarkRead(user string, upto int, now int64) error {
	const op = "MarkRead"
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateUser(op, user); err != nil {
		return err
	}
	if err := validateNow(op, now); err != nil {
		return err
	}
	if upto < 0 {
		return reject(op, ErrInvalidParam, "upto %d < 0", upto)
	}
	if err := c.checkClock(op, now); err != nil {
		return err
	}
	m, err := c.requireMember(op, user)
	if err != nil {
		return err
	}
	if upto < m.watermark {
		return reject(op, ErrWatermarkRollback, "upto %d < watermark %d", upto, m.watermark)
	}
	if upto > len(c.messages) {
		return reject(op, ErrOutOfRange, "upto %d > latest %d", upto, len(c.messages))
	}
	m.watermark = upto
	c.lastNow = now
	return nil
}

// Unread counts messages with seq above the user's watermark that are not
// recalled and not authored by the user. Cost is O(log H), independent of
// the total history length H.
//
// Because seqs are dense in [1, latest]:
//
//	unread = (latest - watermark)
//	       - recalled seqs above watermark
//	       - own non-recalled seqs above watermark
func (c *Channel) Unread(user string) (int, error) {
	const op = "Unread"
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateUser(op, user); err != nil {
		return 0, err
	}
	m, err := c.requireMember(op, user)
	if err != nil {
		return 0, err
	}
	w := m.watermark
	unread := len(c.messages) - w -
		c.recalledSeqs.countGreater(w) -
		c.ownActive[user].countGreater(w)
	return unread, nil
}

// UnreadMentions counts unread messages (same rule as Unread) whose current
// mention set contains the user. The mention index always reflects the
// current mention set, so edits and recalls take effect immediately, while
// messages at or below the watermark never count. Cost is O(log H).
func (c *Channel) UnreadMentions(user string) (int, error) {
	const op = "UnreadMentions"
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateUser(op, user); err != nil {
		return 0, err
	}
	m, err := c.requireMember(op, user)
	if err != nil {
		return 0, err
	}
	return c.mentionIndex[user].countGreater(m.watermark), nil
}

// Fetch returns up to limit messages with seq strictly below before
// (before == 0 starts from the latest), in descending seq order, restricted
// to seqs above the viewer's join watermark. Recalled messages appear as
// placeholders. Fetch is an accepted operation and advances the clock.
func (c *Channel) Fetch(viewer string, before, limit int, now int64) ([]MessageView, error) {
	const op = "Fetch"
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateUser(op, viewer); err != nil {
		return nil, err
	}
	if err := validateNow(op, now); err != nil {
		return nil, err
	}
	if before < 0 {
		return nil, reject(op, ErrInvalidParam, "before %d < 0", before)
	}
	if limit < 1 || limit > MaxFetchLimit {
		return nil, reject(op, ErrInvalidParam, "limit %d out of [1,%d]", limit, MaxFetchLimit)
	}
	if err := c.checkClock(op, now); err != nil {
		return nil, err
	}
	m, err := c.requireMember(op, viewer)
	if err != nil {
		return nil, err
	}

	start := before - 1
	if before == 0 || start > len(c.messages) {
		start = len(c.messages)
	}
	floor := m.joinWatermark
	views := make([]MessageView, 0, limit)
	for seq := start; seq > floor && len(views) < limit; seq-- {
		views = append(views, viewOf(seq, &c.messages[seq-1]))
	}
	c.lastNow = now
	return views, nil
}
