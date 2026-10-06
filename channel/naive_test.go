package channel_test

// naive 是与生产实现完全独立的参考模型：
// 每条消息全量保存，所有计数与查询都以线性扫描重算。
// 它刻意不共享任何生产代码，用于随机差分测试交叉验证。

type naiveMsg struct {
	seq        int64
	author     string
	body       string
	mentions   []string
	createdAt  int64
	editedAt   int64
	editCount  int
	recalled   bool
	recalledBy string
	recalledAt int64
}

type naiveMember struct {
	admin     bool
	watermark int64
}

type naiveChannel struct {
	editWindow   int64
	recallWindow int64
	maxEdits     int

	members  map[string]*naiveMember
	messages []*naiveMsg
	lastNow  int64
}

func newNaive(editWindow, recallWindow int64, maxEdits int) *naiveChannel {
	return &naiveChannel{
		editWindow:   editWindow,
		recallWindow: recallWindow,
		maxEdits:     maxEdits,
		members:      map[string]*naiveMember{},
		lastNow:      -1,
	}
}

const (
	nErrInvalidArg    = "invalid argument"
	nErrClockRewind   = "clock rewind"
	nErrNotMember     = "not a member"
	nErrNotFound      = "message not found"
	nErrForbidden     = "permission denied"
	nErrRecalled      = "message recalled"
	nErrTimeout       = "edit/recall window expired"
	nErrEditLimit     = "edit count limit reached"
	nErrWatermarkBack = "read watermark moved backward"
	nErrOutOfRange    = "watermark beyond latest"
	nOK               = ""
)

func (n *naiveChannel) checkNow(now int64) string {
	if now < 0 || now > 1_000_000_000_000 {
		return nErrInvalidArg
	}
	if now < n.lastNow {
		return nErrClockRewind
	}
	return nOK
}

func validBodyNaive(body string) bool {
	return body != "" && len([]rune(body)) <= 4000
}

func (n *naiveChannel) normalizeMentions(mentions []string, author string) ([]string, string) {
	if len(mentions) > 20 {
		return nil, nErrInvalidArg
	}
	seen := map[string]bool{}
	out := []string{}
	for _, m := range mentions {
		if m == "" || m == author || seen[m] {
			return nil, nErrInvalidArg
		}
		if _, ok := n.members[m]; !ok {
			return nil, nErrInvalidArg
		}
		seen[m] = true
		out = append(out, m)
	}
	return out, nOK
}

func (n *naiveChannel) Join(user string, now int64) string {
	if user == "" {
		return nErrInvalidArg
	}
	if e := n.checkNow(now); e != nOK {
		return e
	}
	if _, ok := n.members[user]; ok {
		return nErrInvalidArg
	}
	n.members[user] = &naiveMember{
		admin:     len(n.members) == 0,
		watermark: int64(len(n.messages)),
	}
	n.lastNow = now
	return nOK
}

func (n *naiveChannel) Leave(user string, now int64) string {
	if user == "" {
		return nErrInvalidArg
	}
	if e := n.checkNow(now); e != nOK {
		return e
	}
	if _, ok := n.members[user]; !ok {
		return nErrNotMember
	}
	delete(n.members, user)
	n.lastNow = now
	return nOK
}

func (n *naiveChannel) Promote(actor, target string, now int64) string {
	if actor == "" || target == "" {
		return nErrInvalidArg
	}
	if e := n.checkNow(now); e != nOK {
		return e
	}
	a, ok := n.members[actor]
	if !ok {
		return nErrNotMember
	}
	if !a.admin {
		return nErrForbidden
	}
	t, ok := n.members[target]
	if !ok {
		return nErrNotFound
	}
	t.admin = true
	n.lastNow = now
	return nOK
}

func (n *naiveChannel) Send(user, body string, mentions []string, now int64) (int64, string) {
	if user == "" || !validBodyNaive(body) {
		return 0, nErrInvalidArg
	}
	if e := n.checkNow(now); e != nOK {
		return 0, e
	}
	if _, ok := n.members[user]; !ok {
		return 0, nErrNotMember
	}
	clean, e := n.normalizeMentions(mentions, user)
	if e != nOK {
		return 0, e
	}
	seq := int64(len(n.messages)) + 1
	n.messages = append(n.messages, &naiveMsg{
		seq:       seq,
		author:    user,
		body:      body,
		mentions:  clean,
		createdAt: now,
	})
	n.lastNow = now
	return seq, nOK
}

func (n *naiveChannel) Edit(user string, seq int64, body string, mentions []string, now int64) string {
	if user == "" || seq <= 0 || !validBodyNaive(body) {
		return nErrInvalidArg
	}
	if e := n.checkNow(now); e != nOK {
		return e
	}
	if _, ok := n.members[user]; !ok {
		return nErrNotMember
	}
	if seq > int64(len(n.messages)) {
		return nErrNotFound
	}
	msg := n.messages[seq-1]
	if msg.author != user {
		return nErrForbidden
	}
	clean, e := n.normalizeMentions(mentions, user)
	if e != nOK {
		return e
	}
	if msg.recalled {
		return nErrRecalled
	}
	if now-msg.createdAt >= n.editWindow {
		return nErrTimeout
	}
	if msg.editCount >= n.maxEdits {
		return nErrEditLimit
	}
	msg.body = body
	msg.mentions = clean
	msg.editedAt = now
	msg.editCount++
	n.lastNow = now
	return nOK
}

func (n *naiveChannel) Recall(user string, seq, now int64) string {
	if user == "" || seq <= 0 {
		return nErrInvalidArg
	}
	if e := n.checkNow(now); e != nOK {
		return e
	}
	caller, ok := n.members[user]
	if !ok {
		return nErrNotMember
	}
	if seq > int64(len(n.messages)) {
		return nErrNotFound
	}
	msg := n.messages[seq-1]
	if msg.author != user && !caller.admin {
		return nErrForbidden
	}
	if msg.recalled {
		return nErrRecalled
	}
	if !caller.admin && now-msg.createdAt >= n.recallWindow {
		return nErrTimeout
	}
	msg.recalled = true
	msg.recalledBy = user
	msg.recalledAt = now
	msg.body = ""
	msg.mentions = nil
	n.lastNow = now
	return nOK
}

func (n *naiveChannel) MarkRead(user string, upto, now int64) string {
	if user == "" || upto < 0 {
		return nErrInvalidArg
	}
	if e := n.checkNow(now); e != nOK {
		return e
	}
	m, ok := n.members[user]
	if !ok {
		return nErrNotMember
	}
	if upto < m.watermark {
		return nErrWatermarkBack
	}
	if upto > int64(len(n.messages)) {
		return nErrOutOfRange
	}
	m.watermark = upto
	n.lastNow = now
	return nOK
}

func (n *naiveChannel) unread(user string) (int64, string) {
	m, ok := n.members[user]
	if !ok {
		return 0, nErrNotMember
	}
	var count int64
	for _, msg := range n.messages {
		if msg.seq > m.watermark && !msg.recalled && msg.author != user {
			count++
		}
	}
	return count, nOK
}

func (n *naiveChannel) unreadMentions(user string) (int64, string) {
	m, ok := n.members[user]
	if !ok {
		return 0, nErrNotMember
	}
	var count int64
	for _, msg := range n.messages {
		if msg.seq > m.watermark && !msg.recalled {
			for _, mentioned := range msg.mentions {
				if mentioned == user {
					count++
					break
				}
			}
		}
	}
	return count, nOK
}

type naiveItem struct {
	seq        int64
	recalled   bool
	author     string
	body       string
	mentions   []string
	createdAt  int64
	editedAt   int64
	editCount  int
	recalledBy string
	recalledAt int64
}

func (n *naiveChannel) fetch(viewer string, before, limit, now int64) ([]naiveItem, string) {
	if limit < 1 || limit > 100 || before < 0 {
		return nil, nErrInvalidArg
	}
	if e := n.checkNow(now); e != nOK {
		return nil, e
	}
	m, ok := n.members[viewer]
	if !ok {
		return nil, nErrNotMember
	}
	latest := int64(len(n.messages))
	high := before - 1
	if before == 0 {
		high = latest
	}
	if high > latest {
		high = latest
	}
	low := m.watermark + 1
	if high < low {
		return []naiveItem{}, nOK
	}
	count := high - low + 1
	if count > limit {
		count = limit
	}
	items := []naiveItem{}
	for i := int64(0); i < count; i++ {
		msg := n.messages[high-i-1]
		items = append(items, naiveItem{
			seq:        msg.seq,
			recalled:   msg.recalled,
			author:     msg.author,
			body:       msg.body,
			mentions:   append([]string(nil), msg.mentions...),
			createdAt:  msg.createdAt,
			editedAt:   msg.editedAt,
			editCount:  msg.editCount,
			recalledBy: msg.recalledBy,
			recalledAt: msg.recalledAt,
		})
	}
	return items, nOK
}
