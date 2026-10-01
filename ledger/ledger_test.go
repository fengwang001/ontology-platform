package ledger

import (
	"errors"
	"strings"
	"testing"
)

func reasonOf(err error) string {
	if err == nil {
		return "ok"
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Reason.String()
	}
	return "unexpected error: " + err.Error()
}

// assertEquivalent 逐步对照 Ledger 与朴素模型的全部可观测状态，
// 并校验“未确认数 <= P”与“队列/未确认/已结算互不相交且并集为全部消息”。
func assertEquivalent(t *testing.T, l *Ledger[string], m *naiveModel, step string) {
	t.Helper()

	if l.MaxTag() != uint64(m.maxTag) {
		t.Fatalf("[%s] maxTag: ledger=%d model=%d", step, l.MaxTag(), m.maxTag)
	}
	if l.UnackedCount() != len(m.unacked) {
		t.Fatalf("[%s] unackedCount: ledger=%d model=%d", step, l.UnackedCount(), len(m.unacked))
	}
	if l.QueueLen() != len(m.queue) {
		t.Fatalf("[%s] queueLen: ledger=%d model=%d", step, l.QueueLen(), len(m.queue))
	}
	if l.Dropped() != m.dropped {
		t.Fatalf("[%s] dropped: ledger=%d model=%d", step, l.Dropped(), m.dropped)
	}
	if l.UnackedCount() > l.Prefetch() {
		t.Fatalf("[%s] unacked %d exceeds prefetch %d", step, l.UnackedCount(), l.Prefetch())
	}

	gotQueue := l.QueuedMessages()
	wantQueue := make([]string, 0, len(m.queue))
	for _, seq := range m.queue {
		wantQueue = append(wantQueue, m.messages[seq])
	}
	if strings.Join(gotQueue, ",") != strings.Join(wantQueue, ",") {
		t.Fatalf("[%s] queue order: ledger=%v model=%v", step, gotQueue, wantQueue)
	}

	gotUnacked := l.Unacked()
	if len(gotUnacked) != len(m.unacked) {
		t.Fatalf("[%s] unacked snapshot len mismatch", step)
	}
	for _, d := range gotUnacked {
		seq, ok := m.unacked[int(d.Tag)]
		if !ok {
			t.Fatalf("[%s] ledger has unacked tag %d absent from model", step, d.Tag)
		}
		if int(d.Seq) != seq || d.Message != m.messages[seq] || d.Redeliver != m.redeliv[seq] {
			t.Fatalf("[%s] unacked tag %d mismatch: ledger=(seq=%d,msg=%q,red=%v) model=(seq=%d,msg=%q,red=%v)",
				step, d.Tag, d.Seq, d.Message, d.Redeliver, seq, m.messages[seq], m.redeliv[seq])
		}
	}

	// 三态划分不变量：queued + unacked + settled == 全部入队消息。
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) != len(m.messages) {
		t.Fatalf("[%s] total entries: ledger=%d model(enqueued)=%d", step, len(l.entries), len(m.messages))
	}
	queued, unacked, settled := 0, 0, 0
	for _, e := range l.entries {
		switch e.phase {
		case phaseQueued:
			queued++
		case phaseUnacked:
			unacked++
		case phaseSettled:
			settled++
		}
	}
	if queued != len(m.queue) || unacked != len(m.unacked) || queued+unacked+settled != len(m.messages) {
		t.Fatalf("[%s] partition broken: queued=%d/%d unacked=%d/%d settled=%d total=%d",
			step, queued, len(m.queue), unacked, len(m.unacked), settled, len(m.messages))
	}
}
