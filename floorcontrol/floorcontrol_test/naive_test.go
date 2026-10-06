package floorcontrol_test

// naive 是与生产实现完全独立编写的朴素参考模型：
// 队列用 []string 保存（允许 O(n) 操作），状态判定与惰性到期
// 逻辑按规范文字直接实现，与 treap 版本不共享任何代码。

type naiveRole int

const (
	nAttendee naiveRole = iota
	nCoManager
	nHost
)

type naiveMember struct {
	role     naiveRole
	muted    bool
	joinedAt int64
}

type naiveRoom struct {
	s, qcap int64
	closed  bool
	members map[string]naiveMember
	joinSeq []string
	host    string
	queue   []string
	speaker string
	expire  int64
	lastNow int64
	clockOn bool
}

func newNaive(s int64, q int) *naiveRoom {
	return &naiveRoom{s: s, qcap: int64(q), members: map[string]naiveMember{}}
}

func (n *naiveRoom) advance(now int64) {
	n.lastNow = now
	n.clockOn = true
	for !n.closed && n.speaker != "" && n.expire <= now {
		at := n.expire
		n.speaker = ""
		if len(n.queue) > 0 {
			head := n.queue[0]
			n.queue = n.queue[1:]
			n.speaker = head
			n.expire = at + n.s
			continue
		}
		break
	}
}

func (n *naiveRoom) inQueue(u string) int {
	for i, v := range n.queue {
		if v == u {
			return i
		}
	}
	return -1
}

func (n *naiveRoom) removeFromQueue(u string) {
	i := n.inQueue(u)
	if i >= 0 {
		n.queue = append(n.queue[:i], n.queue[i+1:]...)
	}
}

func (n *naiveRoom) grantHead(at int64) {
	if len(n.queue) == 0 {
		return
	}
	head := n.queue[0]
	n.queue = n.queue[1:]
	n.speaker = head
	n.expire = at + n.s
}

type nExec func(*naiveRoom) error

// begin 返回一个已完成时间推进（若通过前两关）的执行闭包。
func (n *naiveRoom) pre(op operator, now int64) (nExec, error) {
	invalid := op.user == "" || now < 0 || now > 1_000_000_000_000
	if invalid {
		return nil, errInvalid
	}
	if n.clockOn && now < n.lastNow {
		return nil, errClock
	}
	n.advance(now)
	if n.closed {
		return nil, errClosed
	}
	if _, ok := n.members[op.user]; !ok {
		return nil, errNoOperator
	}
	return nil, nil
}

func (n *naiveRoom) join(u string, now int64) error {
	if u == "" || now < 0 || now > 1_000_000_000_000 {
		return errInvalid
	}
	if n.clockOn && now < n.lastNow {
		return errClock
	}
	n.advance(now)
	if n.closed {
		return errClosed
	}
	if _, ok := n.members[u]; ok {
		return errAlreadyIn
	}
	role := nAttendee
	if len(n.members) == 0 {
		role = nHost
		n.host = u
	}
	n.members[u] = naiveMember{role: role, joinedAt: now}
	n.joinSeq = append(n.joinSeq, u)
	return nil
}

func (n *naiveRoom) leave(u string, now int64) error {
	if _, e := n.pre(operator{user: u}, now); e != nil {
		return e
	}
	m := n.members[u]
	n.removeFromQueue(u)
	if n.speaker == u {
		n.speaker = ""
		n.grantHead(now)
	}
	delete(n.members, u)
	out := n.joinSeq[:0]
	for _, v := range n.joinSeq {
		if v != u {
			out = append(out, v)
		}
	}
	n.joinSeq = out
	if m.role == nHost {
		var succ string
		for _, v := range n.joinSeq {
			if n.members[v].role == nCoManager {
				succ = v
				break
			}
		}
		if succ == "" {
			if len(n.joinSeq) == 0 {
				n.closed = true
				n.host = ""
				n.speaker = ""
				n.queue = nil
				n.members = map[string]naiveMember{}
				n.joinSeq = nil
				return nil
			}
			succ = n.joinSeq[0]
		}
		m := n.members[succ]
		m.role = nHost
		n.members[succ] = m
		n.host = succ
	}
	return nil
}

func (n *naiveRoom) raise(u string, now int64) error {
	if _, e := n.pre(operator{user: u}, now); e != nil {
		return e
	}
	m := n.members[u]
	switch {
	case n.speaker == u:
		return errSpeaking
	case m.muted:
		return errMutedRaise
	case n.inQueue(u) >= 0:
		return errInQueue
	case int64(len(n.queue)) >= n.qcap:
		return errFull
	}
	n.queue = append(n.queue, u)
	return nil
}

func (n *naiveRoom) lower(u string, now int64) error {
	if _, e := n.pre(operator{user: u}, now); e != nil {
		return e
	}
	if n.inQueue(u) < 0 {
		return errNotQueued
	}
	n.removeFromQueue(u)
	return nil
}

func (n *naiveRoom) roleChange(op, tgt string, now int64, appoint bool) error {
	if op == "" || tgt == "" || now < 0 || now > 1_000_000_000_000 {
		return errInvalid
	}
	if _, e := n.pre(operator{user: op}, now); e != nil {
		return e
	}
	if n.members[op].role != nHost {
		return errPerm
	}
	t, ok := n.members[tgt]
	if !ok {
		return errNoTarget
	}
	switch {
	case t.role == nHost:
		return errTargetHost
	case appoint && t.role == nCoManager:
		return errRoleSame
	case !appoint && t.role == nAttendee:
		return errRoleSame
	}
	if appoint {
		t.role = nCoManager
	} else {
		t.role = nAttendee
	}
	n.members[tgt] = t
	return nil
}

func (n *naiveRoom) grant(op string, now int64) error {
	if _, e := n.pre(operator{user: op}, now); e != nil {
		return e
	}
	r := n.members[op].role
	if r != nHost && r != nCoManager {
		return errPerm
	}
	if n.speaker != "" {
		return errHasSpeaker
	}
	if len(n.queue) == 0 {
		return errEmpty
	}
	n.grantHead(now)
	return nil
}

func (n *naiveRoom) yield(u string, now int64) error {
	if _, e := n.pre(operator{user: u}, now); e != nil {
		return e
	}
	if n.speaker != u {
		return errNotSpeaking
	}
	n.speaker = ""
	return nil
}

func (n *naiveRoom) mute(op, tgt string, now int64, mute bool) error {
	if op == "" || tgt == "" || now < 0 || now > 1_000_000_000_000 {
		return errInvalid
	}
	if _, e := n.pre(operator{user: op}, now); e != nil {
		return e
	}
	if n.members[op].role != nHost {
		return errPerm
	}
	t, ok := n.members[tgt]
	if !ok {
		return errNoTarget
	}
	switch {
	case t.role == nHost:
		return errTargetHost
	case mute && t.muted:
		return errAlreadyMuted
	case !mute && !t.muted:
		return errNotMuted
	}
	t.muted = mute
	n.members[tgt] = t
	if mute {
		if n.speaker == tgt {
			n.speaker = ""
			n.grantHead(now)
		}
		n.removeFromQueue(tgt)
	}
	return nil
}

type naiveSnapshot struct {
	host      string
	speaker   string
	remaining int64
	queue     []string
	members   []naiveMember
	joinSeq   []string
}

func (n *naiveRoom) snapshot(now int64) (naiveSnapshot, error) {
	if now < 0 || now > 1_000_000_000_000 {
		return naiveSnapshot{}, errInvalid
	}
	if n.clockOn && now < n.lastNow {
		return naiveSnapshot{}, errClock
	}
	n.advance(now)
	if n.closed {
		return naiveSnapshot{}, errClosed
	}
	s := naiveSnapshot{
		host:    n.host,
		speaker: n.speaker,
		queue:   append([]string{}, n.queue...),
		joinSeq: append([]string{}, n.joinSeq...),
	}
	if n.speaker != "" {
		s.remaining = n.expire - now
	}
	for _, u := range n.joinSeq {
		s.members = append(s.members, n.members[u])
	}
	return s, nil
}

func (n *naiveRoom) queuePos(u string, now int64) (int, error) {
	if u == "" {
		return 0, errInvalid
	}
	if now < 0 || now > 1_000_000_000_000 {
		return 0, errInvalid
	}
	if n.clockOn && now < n.lastNow {
		return 0, errClock
	}
	n.advance(now)
	if n.closed {
		return 0, errClosed
	}
	if _, ok := n.members[u]; !ok {
		return 0, errNoTarget
	}
	i := n.inQueue(u)
	if i < 0 {
		return 0, errNotQueued
	}
	return i + 1, nil
}
