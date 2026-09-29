package outbox

import "sync"

// Sink 是下游投递接口，实现方需按消息标识幂等去重。
type Sink interface {
	Apply(msg Message) error
}

// IdempotentSink 按消息标识幂等去重的下游，记录实际应用序列。
type IdempotentSink struct {
	mu      sync.Mutex
	seen    map[string]struct{}
	applied []Message
}

func NewIdempotentSink() *IdempotentSink {
	return &IdempotentSink{seen: make(map[string]struct{})}
}

// Apply 幂等应用消息：重复标识直接丢弃，返回是否为重复。
func (s *IdempotentSink) Apply(msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked(msg)
}

func (s *IdempotentSink) applyLocked(msg Message) error {
	if _, dup := s.seen[msg.ID]; dup {
		return nil
	}
	s.seen[msg.ID] = struct{}{}
	s.applied = append(s.applied, msg)
	return nil
}

// IsDuplicate 报告消息标识是否已被下游应用过。
func (s *IdempotentSink) IsDuplicate(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, dup := s.seen[id]
	return dup
}

// Applied 返回下游实际应用的消息序列（去重后）。
func (s *IdempotentSink) Applied() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, len(s.applied))
	copy(out, s.applied)
	return out
}

// Relay 把发件箱中已提交未标记的消息按提交序与写入序投递给下游，
// 每条都是先投递后标记。
type Relay struct {
	store *Store
	sink  Sink
	// crashBeforeLastMark 模拟崩溃：最后一条已投递但未标记即返回。
	crashBeforeLastMark bool
}

func NewRelay(store *Store, sink Sink) *Relay {
	return &Relay{store: store, sink: sink}
}

// SetCrashBeforeLastMark 开启/关闭崩溃点模拟（最后一条已投递未标记）。
func (r *Relay) SetCrashBeforeLastMark(v bool) {
	r.crashBeforeLastMark = v
}

// RunOnce 取出全部已提交未标记消息，按 (commitSeq, writeSeq) 逐条
// 先投递后标记。返回本次投递的消息序列。
func (r *Relay) RunOnce() []Message {
	batch := r.store.Pending()
	delivered := make([]Message, 0, len(batch))
	for i, msg := range batch {
		if err := r.sink.Apply(msg); err != nil {
			// 投递失败：停止本轮，未标记的消息留待下次重投。
			break
		}
		delivered = append(delivered, msg)
		if r.crashBeforeLastMark && i == len(batch)-1 {
			// 崩溃点：最后一条已投递但未标记，重启后会重投，
			// 由下游按消息标识幂等去重。
			return delivered
		}
		r.store.MarkDelivered(msg.ID)
	}
	return delivered
}
