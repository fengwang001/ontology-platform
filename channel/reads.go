package channel

// unreadRange 返回某成员读水位之后区间 (watermark, latest] 的相关计数。
// 调用方已持有锁；计数树可能尚未创建（nil 安全）。
func (c *Channel) rangeAfter(b *fenwick, watermark int64) int64 {
	latest := int64(len(c.messages))
	if b == nil || watermark >= latest {
		return 0
	}
	return b.rangeSum(watermark+1, latest)
}

// MarkRead 把读水位推进到 upto。
// upto<当前水位报水位回退；upto>最新序号报越界；恰等于当前水位成功且无变化。
func (c *Channel) MarkRead(user string, upto, now int64) error {
	if user == "" || upto < 0 {
		return ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.validateNow(now); err != nil {
		return err
	}
	m, ok := c.members[user]
	if !ok {
		return ErrNotMember
	}
	latest := int64(len(c.messages))
	if upto < m.watermark {
		return ErrWatermarkBack
	}
	if upto > latest {
		return ErrOutOfRange
	}
	m.watermark = upto
	c.lastNow = now
	return nil
}

// Unread 返回序号大于水位、未撤回、且作者不是该成员本人的消息条数。
// 两次 Fenwick 区间查询完成，与历史消息总数无关。
func (c *Channel) Unread(user string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.members[user]
	if !ok {
		return 0, ErrNotMember
	}
	alive := c.rangeAfter(c.aliveBit, m.watermark)
	own := c.rangeAfter(c.authoredBit[user], m.watermark)
	return alive - own, nil
}

// UnreadMentions 返回 Unread 口径下当前提及集合含该成员的消息条数。
// 提及计数树在撤回时 -1、在编辑增删时同步增减，因此按当前集合精确判定。
func (c *Channel) UnreadMentions(user string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.members[user]
	if !ok {
		return 0, ErrNotMember
	}
	// 自己不可能提及自己，故提及树天然不含本人作者消息，无需再减 own。
	return c.rangeAfter(c.mentionBit[user], m.watermark), nil
}

// Fetch 返回序号严格小于 before 的最多 limit 条消息，按序号降序；
// 只含序号大于 viewer 加入时水位的消息。撤回消息以占位形式出现。
// before=0 表示从最新开始。非成员不得 Fetch。
func (c *Channel) Fetch(viewer string, before, limit int64, now int64) ([]Item, error) {
	if limit < 1 || limit > maxFetchLimit || before < 0 {
		return nil, ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.validateNow(now); err != nil {
		return nil, err
	}
	m, ok := c.members[viewer]
	if !ok {
		return nil, ErrNotMember
	}

	latest := int64(len(c.messages))
	high := before - 1 // 严格小于 before
	if before == 0 {
		high = latest
	}
	if high > latest {
		high = latest
	}
	low := m.watermark + 1 // 仅含加入水位之后的消息
	if high < low {
		return []Item{}, nil
	}

	count := high - low + 1
	if count > limit {
		count = limit
	}
	items := make([]Item, 0, count)
	for i := int64(0); i < count; i++ {
		seq := high - i
		msg := c.messages[seq-1]
		items = append(items, c.toItem(msg))
	}
	return items, nil
}

// toItem 在锁内把存储消息投影为对外视图。
func (c *Channel) toItem(msg *storedMessage) Item {
	if msg.recalled {
		return Item{
			Seq: msg.seq,
			Placeholder: &Placeholder{
				Seq:        msg.seq,
				RecalledBy: msg.recalledBy,
				RecalledAt: msg.recalledAt,
			},
		}
	}
	mentions := append([]string(nil), msg.mentions...)
	return Item{
		Seq: msg.seq,
		Message: &Message{
			Seq:       msg.seq,
			Author:    msg.author,
			Body:      msg.body,
			Mentions:  mentions,
			CreatedAt: msg.createdAt,
			EditedAt:  msg.editedAt,
			EditCount: msg.editCount,
		},
	}
}
