package delivery

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// modelLedger is a deliberately naive, step-by-step simulation of the
// specification. It keeps three plain lists (queue, unacked, settled) and
// uses linear scans everywhere, so it can be cross-checked against the
// real Ledger on identical call sequences.
type modelLedger struct {
	prefetch int
	nextSeq  uint64
	nextTag  uint64
	queue    []modelMsg
	unacked  []modelDelivery
	settled  []modelDelivery
	dropped  uint64
}

type modelMsg struct {
	seq      uint64
	payload  any
	requeued bool
}

type modelDelivery struct {
	tag uint64
	msg modelMsg
}

func newModel(prefetch int) *modelLedger {
	return &modelLedger{prefetch: prefetch, nextSeq: 1, nextTag: 1}
}

func (m *modelLedger) Enqueue(payload any) uint64 {
	seq := m.nextSeq
	m.nextSeq++
	msg := modelMsg{seq: seq, payload: payload}
	// Keep the queue ordered by enqueue sequence: insert before the first
	// message with a larger seq.
	i := 0
	for i < len(m.queue) && m.queue[i].seq < seq {
		i++
	}
	m.queue = append(m.queue, modelMsg{})
	copy(m.queue[i+1:], m.queue[i:])
	m.queue[i] = msg
	return seq
}

func (m *modelLedger) Deliver() (Delivery, error) {
	// Prefetch check first: it wins over the empty-queue case.
	if len(m.unacked) >= m.prefetch {
		return Delivery{}, ErrPrefetchFull
	}
	if len(m.queue) == 0 {
		return Delivery{}, ErrQueueEmpty
	}
	msg := m.queue[0]
	m.queue = m.queue[1:]
	tag := m.nextTag
	m.nextTag++
	m.unacked = append(m.unacked, modelDelivery{tag: tag, msg: msg})
	return Delivery{Tag: tag, Seq: msg.seq, Payload: msg.payload, Redelivered: msg.requeued}, nil
}

// pick validates the tag and returns the indexes into m.unacked that the
// operation covers, in ascending tag order.
func (m *modelLedger) pick(tag uint64, multiple bool) ([]int, error) {
	if tag == 0 || tag >= m.nextTag {
		return nil, ErrInvalidTag
	}
	var idx []int
	for i, d := range m.unacked {
		if d.tag <= tag && (multiple || d.tag == tag) {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		if !multiple {
			return nil, ErrAlreadySettled
		}
		return nil, ErrEmptyRange
	}
	return idx, nil
}

func (m *modelLedger) removeUnacked(idx []int) []modelDelivery {
	picked := make([]modelDelivery, 0, len(idx))
	for _, i := range idx {
		picked = append(picked, m.unacked[i])
	}
	keep := m.unacked[:0]
	marked := make(map[int]bool, len(idx))
	for _, i := range idx {
		marked[i] = true
	}
	for i, d := range m.unacked {
		if !marked[i] {
			keep = append(keep, d)
		}
	}
	m.unacked = keep
	return picked
}

func (m *modelLedger) Ack(tag uint64, multiple bool) (int, error) {
	idx, err := m.pick(tag, multiple)
	if err != nil {
		return 0, err
	}
	picked := m.removeUnacked(idx)
	m.settled = append(m.settled, picked...)
	return len(picked), nil
}

func (m *modelLedger) Reject(tag uint64, multiple bool, requeue bool) (int, error) {
	idx, err := m.pick(tag, multiple)
	if err != nil {
		return 0, err
	}
	picked := m.removeUnacked(idx)
	for _, d := range picked {
		if requeue {
			msg := d.msg
			msg.requeued = true
			i := 0
			for i < len(m.queue) && m.queue[i].seq < msg.seq {
				i++
			}
			m.queue = append(m.queue, modelMsg{})
			copy(m.queue[i+1:], m.queue[i:])
			m.queue[i] = msg
		} else {
			m.dropped++
			m.settled = append(m.settled, d)
		}
	}
	return len(picked), nil
}

func (m *modelLedger) Stats() Stats {
	return Stats{
		Prefetch: m.prefetch,
		Queued:   len(m.queue),
		Unacked:  len(m.unacked),
		Dropped:  m.dropped,
		MaxSeq:   m.nextSeq - 1,
		MaxTag:   m.nextTag - 1,
	}
}

func (m *modelLedger) Snapshot() Snapshot {
	snap := Snapshot{UnackedSeqs: make(map[uint64]uint64, len(m.unacked))}
	for _, msg := range m.queue {
		snap.QueueSeqs = append(snap.QueueSeqs, msg.seq)
	}
	for _, d := range m.unacked {
		snap.UnackedSeqs[d.tag] = d.msg.seq
	}
	return snap
}

// ledgerIface is implemented by both Ledger and modelLedger so one script
// can drive either.
type ledgerIface interface {
	Enqueue(any) uint64
	Deliver() (Delivery, error)
	Ack(uint64, bool) (int, error)
	Reject(uint64, bool, bool) (int, error)
	Stats() Stats
	Snapshot() Snapshot
}

type scriptOp struct {
	kind     string
	payload  int
	tag      uint64
	multiple bool
	requeue  bool
}

func buildScript(seed uint64, n int) []scriptOp {
	rng := rand.New(rand.NewSource(int64(seed)))
	ops := make([]scriptOp, 0, n)
	for i := 0; i < n; i++ {
		switch rng.Intn(10) {
		case 0, 1, 2:
			ops = append(ops, scriptOp{kind: "enqueue", payload: rng.Intn(1000)})
		case 3, 4, 5:
			ops = append(ops, scriptOp{kind: "deliver"})
		case 6, 7:
			ops = append(ops, scriptOp{kind: "ack", tag: uint64(rng.Intn(24)), multiple: rng.Intn(2) == 0})
		default:
			ops = append(ops, scriptOp{kind: "reject", tag: uint64(rng.Intn(24)), multiple: rng.Intn(2) == 0, requeue: rng.Intn(2) == 0})
		}
	}
	return ops
}

func (op scriptOp) String() string {
	switch op.kind {
	case "enqueue":
		return fmt.Sprintf("Enqueue(payload=%d)", op.payload)
	case "deliver":
		return "Deliver()"
	case "ack":
		return fmt.Sprintf("Ack(tag=%d, multiple=%v)", op.tag, op.multiple)
	default:
		return fmt.Sprintf("Reject(tag=%d, multiple=%v, requeue=%v)", op.tag, op.multiple, op.requeue)
	}
}

// apply runs the op against l and returns a canonical description of the
// observable result.
func (op scriptOp) apply(l ledgerIface) string {
	switch op.kind {
	case "enqueue":
		return fmt.Sprintf("seq=%d", l.Enqueue(op.payload))
	case "deliver":
		d, err := l.Deliver()
		if err != nil {
			return fmt.Sprintf("err=%v", err)
		}
		return fmt.Sprintf("tag=%d seq=%d payload=%v redelivered=%v", d.Tag, d.Seq, d.Payload, d.Redelivered)
	case "ack":
		n, err := l.Ack(op.tag, op.multiple)
		if err != nil {
			return fmt.Sprintf("err=%v", err)
		}
		return fmt.Sprintf("settled=%d", n)
	default:
		n, err := l.Reject(op.tag, op.multiple, op.requeue)
		if err != nil {
			return fmt.Sprintf("err=%v", err)
		}
		return fmt.Sprintf("affected=%d", n)
	}
}

func decisionBasis(op scriptOp, out string) string {
	switch op.kind {
	case "enqueue":
		return "依据: 入队序号从1起只增不复用, 队列按入队序号持有"
	case "deliver":
		return "依据: 预取已满优先于队列为空; 取入队序号最小者; 标签递增; 回队过的消息重投标志为真"
	case "ack":
		return "依据: 标签非法/已结算/范围为空分别拒绝; 批量按标签<=t取范围; 确认者永久移除"
	default:
		return "依据: 范围取法同确认; 回队按入队序号归位; 否则丢弃并累加丢弃数"
	}
}

func TestAgainstNaiveModel(t *testing.T) {
	for _, prefetch := range []int{1, 2, 4} {
		for seed := uint64(1); seed <= 3; seed++ {
			name := fmt.Sprintf("P=%d/seed=%d", prefetch, seed)
			t.Run(name, func(t *testing.T) {
				real := New(prefetch)
				model := newModel(prefetch)
				ops := buildScript(seed*1000+uint64(prefetch), 400)
				for i, op := range ops {
					gotReal := op.apply(real)
					gotModel := op.apply(model)
					basis := decisionBasis(op, gotReal)
					t.Logf("step %d in: %s -> out: %s (%s)", i, op, gotReal, basis)
					if gotReal != gotModel {
						t.Fatalf("step %d %s: ledger=%q model=%q", i, op, gotReal, gotModel)
					}
					rs, ms := real.Stats(), model.Stats()
					if rs != ms {
						t.Fatalf("step %d %s: stats ledger=%+v model=%+v", i, op, rs, ms)
					}
					rsnap, msnap := real.Snapshot(), model.Snapshot()
					if !reflect.DeepEqual(rsnap, msnap) {
						t.Fatalf("step %d %s: snapshot ledger=%+v model=%+v", i, op, rsnap, msnap)
					}
					if rs.Unacked > prefetch {
						t.Fatalf("step %d: unacked %d exceeds prefetch %d", i, rs.Unacked, prefetch)
					}
				}
				t.Logf("out: %d 步与朴素模拟完全一致, 未确认数始终 <= %d (依据: 逐步对照)", len(ops), prefetch)
			})
		}
	}
}
