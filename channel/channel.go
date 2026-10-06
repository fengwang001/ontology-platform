package channel

import "sync"

// Channel 是群聊频道的全部状态。
//
// 所有公开方法都在同一把互斥锁下串行执行临界区：这给出了可证明的
// 线性一致性（每个调用的整个生效区间内锁被持有），也是重放确定性的
// 基础。查询类调用（Unread 等）同样取锁以读取一致快照。
type Channel struct {
	mu sync.Mutex

	cfg Config

	members map[string]*member
	// messages 按 seq-1 索引；消息只追加、不删除，保证序号无洞。
	messages []*storedMessage

	// aliveBit：未撤回消息计数（撤回 -1）。
	aliveBit *fenwick
	// mentionBit[u]：未撤回且当前提及集合含 u 的消息计数。
	mentionBit map[string]*fenwick
	// authoredBit[u]：u 本人作者的未撤回消息计数。
	authoredBit map[string]*fenwick

	// lastNow 是上一次被接受操作的 now；初始为 -1，允许 now=0。
	lastNow int64
}

// New 创建一个尚无成员的频道。频道将在首个成员 Join 时诞生其管理员。
func New(cfg Config) (*Channel, error) {
	if cfg.EditWindow < 1 || cfg.EditWindow > 86400 ||
		cfg.RecallWindow < 1 || cfg.RecallWindow > 86400 ||
		cfg.MaxEdits < 0 || cfg.MaxEdits > 10 {
		return nil, ErrInvalidArg
	}
	return &Channel{
		cfg:         cfg,
		members:     make(map[string]*member),
		aliveBit:    newFenwick(),
		mentionBit:  make(map[string]*fenwick),
		authoredBit: make(map[string]*fenwick),
		lastNow:     -1,
	}, nil
}

// mentionTree 返回某用户的提及计数树，按需懒创建（Send 成本只与提及人数相关）。
func (c *Channel) mentionTree(user string) *fenwick {
	b := c.mentionBit[user]
	if b == nil {
		b = newFenwick()
		c.mentionBit[user] = b
	}
	return b
}

// authoredTree 返回某用户的作者计数树，按需懒创建。
func (c *Channel) authoredTree(user string) *fenwick {
	b := c.authoredBit[user]
	if b == nil {
		b = newFenwick()
		c.authoredBit[user] = b
	}
	return b
}

// Join/Leave/Promote 见 membership.go；Send/Edit/Recall 见 messages.go；
// MarkRead/Unread/UnreadMentions/Fetch 见 reads.go。

// 以下访问器供测试与文档示例使用。
func (c *Channel) Latest() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return int64(len(c.messages))
}
