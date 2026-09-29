package creditflow

import (
	"bytes"
	"io"
	"log/slog"
	"runtime"
	"sync"
	"testing"
)

// refModel is an intentionally trivial reference implementation: it performs
// one send at a time in an explicit loop and keeps every rule spelled out
// separately. TestReferenceModel replays the identical operation script on
// both the ref model and the real Channel and requires identical observable
// results, making the real auto-send behavior reproducible against the
// straightforward one-message-at-a-time specification.
type refModel struct {
	capacity, limit int64
	credit          int64
	inFlight        int64
	produced        int64
	nextSeq         int64
	nextConsume     int64
	backlog         []*Message
	buffer          []*Message
}

func newRefModel(capacity, limit int64) *refModel {
	return &refModel{capacity: capacity, limit: limit, nextSeq: 1, nextConsume: 1}
}

func (r *refModel) produce(payload []byte) (int64, error) {
	if int64(len(r.backlog)) >= r.limit {
		return 0, ErrBacklogOverflow
	}
	seq := r.nextSeq
	r.nextSeq++
	r.produced++
	r.backlog = append(r.backlog, &Message{Seq: seq, Payload: append([]byte(nil), payload...)})
	for r.credit > 0 && len(r.backlog) > 0 {
		r.credit--
		r.inFlight--
		r.buffer = append(r.buffer, r.backlog[0])
		r.backlog = r.backlog[1:]
	}
	return seq, nil
}

func (r *refModel) advertise() int64 {
	free := r.capacity - int64(len(r.buffer)) - r.inFlight
	if free <= 0 {
		return 0
	}
	r.inFlight += free
	r.credit += free
	for r.credit > 0 && len(r.backlog) > 0 {
		r.credit--
		r.inFlight--
		r.buffer = append(r.buffer, r.backlog[0])
		r.backlog = r.backlog[1:]
	}
	return free
}

func (r *refModel) probe() (int64, error) {
	if r.credit != 0 || len(r.backlog) == 0 {
		return 0, ErrProbeNotAllowed
	}
	return r.advertise(), nil
}

func (r *refModel) consume(seq int64) (*Message, error) {
	if seq != r.nextConsume {
		return nil, ErrConsumeOutOfBounds
	}
	if len(r.buffer) == 0 {
		return nil, ErrConsumeOutOfBounds
	}
	msg := r.buffer[0]
	r.buffer = r.buffer[1:]
	r.nextConsume++
	return msg, nil
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a == b
}

func TestReferenceModel(t *testing.T) {
	const cap, limit = int64(2), int64(6)
	scripts := [][]string{
		{"P", "P", "P", "A", "C", "A", "C", "C", "A", "P", "C"},
		{"P", "PR", "C", "PR", "P", "PR", "C"},
		{"P", "P", "PR", "C", "PR", "C", "P", "A", "C"},
		{"P", "P", "P", "P", "P", "P", "P", "A", "C", "A"},
	}
	for n, script := range scripts {
		lg, _ := testLogger()
		ch, err := New(cap, limit, lg)
		if err != nil {
			t.Fatal(err)
		}
		ref := newRefModel(cap, limit)

		var payloadID int
		for _, op := range script {
			payloadID++
			payload := []byte{byte('A' + payloadID%26)}
			switch op {
			case "P":
				s1, e1 := ch.Produce(payload)
				s2, e2 := ref.produce(payload)
				if s1 != s2 || !sameErr(e1, e2) {
					t.Fatalf("case %d produce: (%d,%v) vs ref (%d,%v)", n, s1, e1, s2, e2)
				}
			case "A":
				if ch.Advertise() != ref.advertise() {
					t.Fatalf("case %d advertise mismatch", n)
				}
			case "PR":
				g1, e1 := ch.Probe()
				g2, e2 := ref.probe()
				if g1 != g2 || !sameErr(e1, e2) {
					t.Fatalf("case %d probe: (%d,%v) vs ref (%d,%v)", n, g1, e1, g2, e2)
				}
			case "C":
				seq := ref.nextConsume
				m1, e1 := ch.Consume(seq)
				m2, e2 := ref.consume(seq)
				if !sameErr(e1, e2) {
					t.Fatalf("case %d consume err: %v vs %v", n, e1, e2)
				}
				if e1 == nil && (m1.Seq != m2.Seq || !bytes.Equal(m1.Payload, m2.Payload)) {
					t.Fatalf("case %d consume msg: %+v vs %+v", n, m1, m2)
				}
			}
			assertMatchesRef(t, n, ch, ref)
			assertInvariants(t, ch)
		}
	}
}

func assertMatchesRef(t *testing.T, n int, ch *Channel, ref *refModel) {
	t.Helper()
	s := ch.Snapshot()
	if s.Credit != ref.credit || s.Backlog != int64(len(ref.backlog)) ||
		s.Buffered != int64(len(ref.buffer)) || s.InFlight != ref.inFlight ||
		s.ProducedTotal != ref.produced || s.ConsumedTotal != ref.nextConsume-1 {
		t.Fatalf("case %d diverges from reference: %+v", n, s)
	}
}

func assertInvariants(t *testing.T, ch *Channel) {
	t.Helper()
	s := ch.Snapshot()
	if s.Credit < 0 {
		t.Fatalf("negative credit: %+v", s)
	}
	if s.Buffered+s.Credit > ch.capacity {
		t.Fatalf("buffered+credit=%d exceeds capacity %d: %+v",
			s.Buffered+s.Credit, ch.capacity, s)
	}
	if s.Buffered+s.InFlight > ch.capacity {
		t.Fatalf("buffered+inFlight=%d exceeds capacity %d: %+v",
			s.Buffered+s.InFlight, ch.capacity, s)
	}
	if s.ProducedTotal != s.Backlog+s.Buffered+s.ConsumedTotal {
		t.Fatalf("produced != backlog+buffered+consumed: %+v", s)
	}
}

// TestConcurrentSenderReceiver runs sender- and receiver-side operations from
// separate goroutines and checks the global invariants afterwards. Run it
// under -race to also prove the locking is sound.
func TestConcurrentSenderReceiver(t *testing.T) {
	// A discard logger keeps tight retry loops from turning into log-write
	// contention; behavior and locking are unchanged.
	lg := slog.New(slog.NewTextHandler(io.Discard, nil))
	const cap, limit = int64(8), int64(500)
	ch, err := New(cap, limit, lg)
	if err != nil {
		t.Fatal(err)
	}

	const total = 300
	var wg sync.WaitGroup
	wg.Add(3)

	// Producer: keeps producing; backlog-overflow rejection is expected and
	// must be harmless.
	go func() {
		defer wg.Done()
		for i := 0; i < total; i++ {
			for {
				if _, err := ch.Produce([]byte{byte(i)}); err == nil {
					break
				}
				runtime.Gosched()
			}
		}
	}()

	// Receiver-advertiser: periodically grants credit based on free slots.
	go func() {
		defer wg.Done()
		var granted int64
		for granted < total {
			g := ch.Advertise()
			if g == 0 {
				// A probe is legal only with zero credit and a non-empty
				// backlog; rejection here is a legal no-op outcome.
				p, _ := ch.Probe()
				granted += p
			}
			granted += g
			runtime.Gosched()
		}
	}()

	// Consumer: consumes in strict sequence.
	go func() {
		defer wg.Done()
		for seq := int64(1); seq <= total; {
			if _, err := ch.Consume(seq); err == nil {
				seq++
			}
			runtime.Gosched()
		}
	}()

	wg.Wait()
	s := ch.Snapshot()
	if s.ProducedTotal != total || s.ConsumedTotal != total {
		t.Fatalf("totals wrong: %+v", s)
	}
	if s.Backlog != 0 || s.Buffered != 0 {
		t.Fatalf("drained channel expected: %+v", s)
	}
	if s.Credit != 0 || s.InFlight != 0 {
		t.Fatalf("credit/inflight not settled: %+v", s)
	}
}
