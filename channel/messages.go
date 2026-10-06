package channel

// normalizeMentions 校验提及集合：非空、至多 20、互不相同、不含作者、
// 全部为当前成员。通过时返回去重后的稳定切片（按首次出现顺序）。
// 调用方已持有锁。
func (c *Channel) normalizeMentions(mentions []string, author string) ([]string, error) {
	if len(mentions) > maxMentions {
		return nil, ErrInvalidArg
	}
	seen := make(map[string]struct{}, len(mentions))
	out := make([]string, 0, len(mentions))
	for _, m := range mentions {
		if m == "" {
			return nil, ErrInvalidArg
		}
		if m == author {
			return nil, ErrInvalidArg
		}
		if _, dup := seen[m]; dup {
			return nil, ErrInvalidArg
		}
		if _, ok := c.members[m]; !ok {
			return nil, ErrInvalidArg
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	return out, nil
}

// validBody 校验正文：非空且按 rune 计不超过 4000 个字符。
func validBody(body string) bool {
	if body == "" {
		return false
	}
	return len([]rune(body)) <= maxBodyLen
}

// Send 发送一条消息。被接受的发送获得频道内从 1 开始的连续序号；
// 被拒绝的发送不占号、不改变时钟。
func (c *Channel) Send(user, body string, mentions []string, now int64) (int64, error) {
	if user == "" || !validBody(body) {
		return 0, ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.validateNow(now); err != nil {
		return 0, err
	}
	_, ok := c.members[user]
	if !ok {
		return 0, ErrNotMember
	}
	cleanMentions, err := c.normalizeMentions(mentions, user)
	if err != nil {
		return 0, err
	}

	seq := int64(len(c.messages)) + 1
	msg := &storedMessage{
		seq:       seq,
		author:    user,
		body:      body,
		mentions:  cleanMentions,
		createdAt: now,
	}
	c.messages = append(c.messages, msg)

	// 索引维护：成本只与提及人数有关，与成员总数、历史长度无关。
	c.aliveBit.add(seq, 1)
	c.authoredTree(user).add(seq, 1)
	for _, m := range cleanMentions {
		c.mentionTree(m).add(seq, 1)
	}

	c.lastNow = now
	return seq, nil
}

// Edit 仅作者可在发出后严格小于 E 秒内整体替换正文与提及；K 为编辑次数上限；
// 已撤回的消息不可编辑。编辑不改变序号。
func (c *Channel) Edit(user string, seq int64, body string, mentions []string, now int64) error {
	if user == "" || seq <= 0 || !validBody(body) {
		return ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.validateNow(now); err != nil {
		return err
	}
	if _, ok := c.members[user]; !ok {
		return ErrNotMember
	}
	if seq > int64(len(c.messages)) {
		return ErrNotFound
	}
	msg := c.messages[seq-1]
	if msg.author != user {
		// 编辑不允许管理员代劳：非作者一律权限不足。
		return ErrForbidden
	}
	cleanMentions, err := c.normalizeMentions(mentions, user)
	if err != nil {
		return err
	}
	// 状态层次序：已撤回 > 超时 > 次数超限。
	if msg.recalled {
		return ErrRecalled
	}
	if now-msg.createdAt >= c.cfg.EditWindow {
		return ErrTimeout
	}
	if msg.editCount >= c.cfg.MaxEdits {
		return ErrEditLimit
	}

	// 按当前提及集合更新每用户提及计数（对称差）。
	old := msg.mentions
	added, removed := diffSets(old, cleanMentions)
	for _, m := range removed {
		c.mentionTree(m).add(seq, -1)
	}
	for _, m := range added {
		c.mentionTree(m).add(seq, 1)
	}

	msg.body = body
	msg.mentions = cleanMentions
	msg.editedAt = now
	msg.editCount++
	c.lastNow = now
	return nil
}

// Recall 撤回消息：作者在发出后严格小于 R 秒内可撤回；管理员可随时撤回
// 任何人的消息。序号保留为占位，正文与提及被清除；重复撤回报已撤回。
func (c *Channel) Recall(user string, seq int64, now int64) error {
	if user == "" || seq <= 0 {
		return ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.validateNow(now); err != nil {
		return err
	}
	caller, ok := c.members[user]
	if !ok {
		return ErrNotMember
	}
	if seq > int64(len(c.messages)) {
		return ErrNotFound
	}
	msg := c.messages[seq-1]
	isAdmin := caller.admin
	if msg.author != user && !isAdmin {
		// 权限不足先于状态判定：无权者对已撤回消息操作仍报权限不足。
		return ErrForbidden
	}
	// 已撤回先于超时：管理员/作者再次撤回报已撤回。
	if msg.recalled {
		return ErrRecalled
	}
	if !isAdmin && now-msg.createdAt >= c.cfg.RecallWindow {
		return ErrTimeout
	}

	msg.recalled = true
	msg.recalledBy = user
	msg.recalledAt = now

	// 撤回使消息同时退出未读与未读提及。
	c.aliveBit.add(seq, -1)
	c.authoredTree(msg.author).add(seq, -1)
	for _, m := range msg.mentions {
		c.mentionTree(m).add(seq, -1)
	}
	msg.body = ""
	msg.mentions = nil

	c.lastNow = now
	return nil
}

// diffSets 返回 b 相对 a 的新增与删除元素（均为去重成员标识）。
func diffSets(a, b []string) (added, removed []string) {
	setA := make(map[string]struct{}, len(a))
	for _, x := range a {
		setA[x] = struct{}{}
	}
	setB := make(map[string]struct{}, len(b))
	for _, x := range b {
		setB[x] = struct{}{}
	}
	for _, x := range b {
		if _, ok := setA[x]; !ok {
			added = append(added, x)
		}
	}
	for _, x := range a {
		if _, ok := setB[x]; !ok {
			removed = append(removed, x)
		}
	}
	return added, removed
}
