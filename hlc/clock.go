package hlc

import (
	"io"
	"log"
	"sort"
	"sync"
)

// Logger 用于记录每一步输入、时间戳与判定依据。
type Logger interface {
	Printf(format string, args ...any)
}

// System 是一组共享消息空间的节点时钟集合，所有方法均可被多个 goroutine 并发调用。
type System struct {
	maxSkew uint64
	logger  Logger
	mu      sync.Mutex

	nodes    map[string]*nodeState
	messages map[string]*message
	events   []*Event
	seq      uint64
}

type nodeState struct {
	name string
	now  Timestamp
}

type message struct {
	id       string
	from     string
	to       string
	sendSeq  uint64
	sendTS   Timestamp
	received bool
	recvSeq  uint64
}

// Option 配置新的 System。
type Option func(*System)

// WithMaxSkew 设置接收事件允许的最大物理偏差（消息逻辑时间 - 本地物理读数的上界），默认 0（不限制）。
func WithMaxSkew(maxSkew uint64) Option {
	return func(s *System) { s.maxSkew = maxSkew }
}

// WithLogger 注入步骤日志记录器。
func WithLogger(l Logger) Option {
	return func(s *System) { s.logger = l }
}

// NewSystem 创建一个空的时钟系统。w 非 nil 时自动写入标准日志格式的步骤日志。
func NewSystem(w io.Writer, opts ...Option) *System {
	s := &System{
		nodes:    make(map[string]*nodeState),
		messages: make(map[string]*message),
		logger:   defaultLogger(w),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// AddNode 创建一个初始时间戳为 (0,0) 的节点。节点名为空或重名返回 ErrInvalidArgument。
func (s *System) AddNode(name string) error {
	if name == "" {
		s.logReject("add_node", "", "", "node name is empty", ErrInvalidArgument)
		return errf(ErrInvalidArgument, "add_node", "", "", "node name is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nodes[name]; ok {
		s.logReject("add_node", name, "", "node already exists", ErrInvalidArgument)
		return errf(ErrInvalidArgument, "add_node", name, "", "node already exists")
	}
	s.nodes[name] = &nodeState{name: name}
	s.logf("add_node node=%s -> clock=(0,0)", name)
	return nil
}

// HasNode 报告节点是否存在。
func (s *System) HasNode(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.nodes[name]
	return ok
}

// Local 在 node 上记录一次本地事件。physical 为该节点当前非负物理读数。
func (s *System) Local(node string, physical int64) (Timestamp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ns, err := s.validateNode("local", node)
	if err != nil {
		return Timestamp{}, err
	}
	phys, err := validatePhysical("local", node, physical)
	if err != nil {
		s.logRejectErr(err)
		return Timestamp{}, err
	}

	ts, reason, oerr := tick(ns.now, phys, Timestamp{}, false)
	if oerr != nil {
		e := errf(ErrCounterOverflow, "local", node, "", "%s", oerr.Error())
		s.logRejectErr(e)
		return Timestamp{}, e
	}
	ns.now = ts
	ev := s.appendEventLocked(node, KindLocal, phys, ts, "", "", "", 0)
	s.logTick("local", node, "", phys, ev.Seq, reason, ts)
	return ts, nil
}

// Send 在 from 节点记录一次发送事件，生成一条只能被 to 恰好接收一次的在途消息。
// messageID 必须非空且在系统范围内唯一。
func (s *System) Send(from, to, messageID string, physical int64) (Timestamp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sender, err := s.validateNode("send", from)
	if err != nil {
		return Timestamp{}, err
	}
	if e := s.validateNodeExists("send", to); e != nil {
		s.logRejectErr(e)
		return Timestamp{}, e
	}
	if messageID == "" {
		e := errf(ErrInvalidArgument, "send", from, "", "message id is empty")
		s.logRejectErr(e)
		return Timestamp{}, e
	}
	if _, ok := s.messages[messageID]; ok {
		e := errf(ErrDuplicateMessageID, "send", from, messageID, "message id already used")
		s.logRejectErr(e)
		return Timestamp{}, e
	}
	phys, err := validatePhysical("send", from, physical)
	if err != nil {
		s.logRejectErr(err)
		return Timestamp{}, err
	}

	ts, reason, oerr := tick(sender.now, phys, Timestamp{}, false)
	if oerr != nil {
		e := errf(ErrCounterOverflow, "send", from, messageID, "%s", oerr.Error())
		s.logRejectErr(e)
		return Timestamp{}, e
	}
	sender.now = ts
	ev := s.appendEventLocked(from, KindSend, phys, ts, messageID, to, "", 0)
	s.messages[messageID] = &message{
		id:      messageID,
		from:    from,
		to:      to,
		sendSeq: ev.Seq,
		sendTS:  ts,
	}
	s.logTick("send", from, messageID, phys, ev.Seq, reason+" -> in-flight to="+to, ts)
	return ts, nil
}

// Receive 在 node 上接收 messageID，按消息携带时间戳与本地物理读数推进时钟。
func (s *System) Receive(node, messageID string, physical int64) (Timestamp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	recv, err := s.validateNode("receive", node)
	if err != nil {
		return Timestamp{}, err
	}
	if messageID == "" {
		e := errf(ErrInvalidArgument, "receive", node, "", "message id is empty")
		s.logRejectErr(e)
		return Timestamp{}, e
	}
	msg, ok := s.messages[messageID]
	if !ok {
		e := errf(ErrMessageNotFound, "receive", node, messageID, "no such message")
		s.logRejectErr(e)
		return Timestamp{}, e
	}
	if msg.received {
		e := errf(ErrMessageAlreadyReceived, "receive", node, messageID,
			"message already received at event #%d", msg.recvSeq)
		s.logRejectErr(e)
		return Timestamp{}, e
	}
	if msg.to != node {
		e := errf(ErrMessageTargetMismatch, "receive", node, messageID,
			"message is addressed to %q", msg.to)
		s.logRejectErr(e)
		return Timestamp{}, e
	}
	phys, err := validatePhysical("receive", node, physical)
	if err != nil {
		s.logRejectErr(err)
		return Timestamp{}, err
	}
	if s.maxSkew > 0 && msg.sendTS.L > phys && msg.sendTS.L-phys > s.maxSkew {
		e := errf(ErrSkewExceeded, "receive", node, messageID,
			"message L=%d exceeds physical=%d by more than max-skew=%d",
			msg.sendTS.L, phys, s.maxSkew)
		s.logRejectErr(e)
		return Timestamp{}, e
	}

	ts, reason, oerr := tick(recv.now, phys, msg.sendTS, true)
	if oerr != nil {
		e := errf(ErrCounterOverflow, "receive", node, messageID, "%s", oerr.Error())
		s.logRejectErr(e)
		return Timestamp{}, e
	}
	recv.now = ts
	msg.received = true
	ev := s.appendEventLocked(node, KindReceive, phys, ts, messageID, "", msg.from, msg.sendSeq)
	msg.recvSeq = ev.Seq
	s.logTick("receive", node, messageID, phys, ev.Seq,
		reason+" <- from="+msg.from+" send#"+uintToString(msg.sendSeq), ts)
	return ts, nil
}

// Now 返回节点当前时间戳的快照。
func (s *System) Now(node string) (Timestamp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, err := s.validateNode("now", node)
	if err != nil {
		return Timestamp{}, err
	}
	return ns.now, nil
}

// History 返回某节点按时间戳严格有序的事件历史副本。
func (s *System) History(node string) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.validateNode("history", node); err != nil {
		return nil, err
	}
	out := make([]Event, 0)
	for _, ev := range s.events {
		if ev.Node == node {
			out = append(out, *ev)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return eventLess(&out[i], &out[j])
	})
	return out, nil
}

// AllHistory 返回全部事件按 (时间戳, 全局序号) 有序的合并历史副本。
func (s *System) AllHistory() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	for i, ev := range s.events {
		out[i] = *ev
	}
	sort.SliceStable(out, func(i, j int) bool {
		return eventLess(&out[i], &out[j])
	})
	return out
}

// Event 返回全局序号对应的事件副本（序号从 1 开始）。
func (s *System) Event(seq uint64) (Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq == 0 || seq > uint64(len(s.events)) {
		return Event{}, false
	}
	return *s.events[seq-1], true
}

// MessageState 描述一条消息的当前状态。
type MessageState struct {
	ID       string
	From     string
	To       string
	SendSeq  uint64
	SendTS   Timestamp
	Received bool
	RecvSeq  uint64
}

// Message 查询消息状态；不存在返回 ok=false。
func (s *System) Message(id string) (MessageState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, ok := s.messages[id]
	if !ok {
		return MessageState{}, false
	}
	return MessageState{
		ID:       msg.id,
		From:     msg.from,
		To:       msg.to,
		SendSeq:  msg.sendSeq,
		SendTS:   msg.sendTS,
		Received: msg.received,
		RecvSeq:  msg.recvSeq,
	}, true
}

// InFlight 返回尚未被接收的消息号快照，顺序不保证。
func (s *System) InFlight() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0)
	for _, msg := range s.messages {
		if !msg.received {
			out = append(out, msg.id)
		}
	}
	return out
}

func defaultLogger(w io.Writer) Logger {
	if w == nil {
		return nil
	}
	return log.New(w, "hlc ", log.LstdFlags|log.Lmicroseconds)
}
