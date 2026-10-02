package scheduler

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

type naiveInterval struct {
	id      ID
	from    Offset
	to      Offset
	next    Offset
	pending map[ID]Offset
}

type naiveBatch struct {
	interval ID
	from     Offset
	to       Offset
	acked    bool
}

type naiveScheduler struct {
	nextID    ID
	nextBatch ID
	watermark Offset
	intervals map[ID]*naiveInterval
	batches   map[ID]*naiveBatch
}

func newNaiveScheduler() *naiveScheduler {
	return &naiveScheduler{intervals: make(map[ID]*naiveInterval), batches: make(map[ID]*naiveBatch)}
}

func (n *naiveScheduler) hold(iv *naiveInterval) Offset {
	hold := iv.next
	for _, from := range iv.pending {
		if from < hold {
			hold = from
		}
	}
	return hold
}

func (n *naiveScheduler) complete(iv *naiveInterval) bool {
	return iv.next == iv.to && len(iv.pending) == 0
}

func (n *naiveScheduler) advance() {
	var minimum *Offset
	for _, iv := range n.intervals {
		if n.complete(iv) {
			continue
		}
		value := n.hold(iv)
		if minimum == nil || value < *minimum {
			minimum = &value
		}
	}
	if minimum != nil && *minimum > n.watermark {
		n.watermark = *minimum
	}
}

func (n *naiveScheduler) add(from, to Offset) (ID, error) {
	if from < 0 || to > MaxOffset || from >= to {
		return 0, ErrInvalidArgument
	}
	if from < n.watermark {
		return 0, ErrBelowWatermark
	}
	n.nextID++
	n.intervals[n.nextID] = &naiveInterval{id: n.nextID, from: from, to: to, next: from, pending: make(map[ID]Offset)}
	n.advance()
	return n.nextID, nil
}

func (n *naiveScheduler) process(id, count Offset) (ProcessResult, error) {
	if id <= 0 || count < 1 || count > MaxBatchSize {
		return ProcessResult{}, ErrInvalidArgument
	}
	iv := n.intervals[id]
	if iv == nil {
		return ProcessResult{}, ErrIntervalNotFound
	}
	if iv.next == iv.to {
		return ProcessResult{}, ErrExhausted
	}
	if count > iv.to-iv.next {
		count = iv.to - iv.next
	}
	from := iv.next
	to := from + count
	iv.next = to
	n.nextBatch++
	n.batches[n.nextBatch] = &naiveBatch{interval: id, from: from, to: to}
	iv.pending[n.nextBatch] = from
	n.advance()
	return ProcessResult{Count: count, From: from, To: to, BatchID: n.nextBatch}, nil
}

func (n *naiveScheduler) split(id, num, den Offset) (ID, error) {
	if id <= 0 || num < 0 || den < 1 || den > MaxDenominator || num > den {
		return 0, ErrInvalidArgument
	}
	iv := n.intervals[id]
	if iv == nil {
		return 0, ErrIntervalNotFound
	}
	if iv.next == iv.to {
		return 0, ErrExhausted
	}
	rem := iv.to - iv.next
	product := new(big.Int).Mul(big.NewInt(rem), big.NewInt(num))
	quotient, remainder := new(big.Int).QuoRem(product, big.NewInt(den), new(big.Int))
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	keep := quotient.Int64()
	if keep < 1 {
		keep = 1
	}
	splitPoint := iv.next + keep
	if splitPoint >= iv.to {
		return 0, ErrCannotSplit
	}
	originalTo := iv.to
	iv.to = splitPoint
	n.nextID++
	n.intervals[n.nextID] = &naiveInterval{id: n.nextID, from: splitPoint, to: originalTo, next: splitPoint, pending: make(map[ID]Offset)}
	n.advance()
	return n.nextID, nil
}

func (n *naiveScheduler) ack(id ID) error {
	if id <= 0 {
		return ErrInvalidArgument
	}
	b := n.batches[id]
	if b == nil {
		return ErrBatchNotFound
	}
	if b.acked {
		return ErrAlreadyAcknowledged
	}
	b.acked = true
	delete(n.intervals[b.interval].pending, id)
	n.advance()
	return nil
}

func (n *naiveScheduler) progress(id ID) (Progress, error) {
	if id <= 0 {
		return Progress{}, ErrIntervalNotFound
	}
	iv := n.intervals[id]
	if iv == nil {
		return Progress{}, ErrIntervalNotFound
	}
	return Progress{From: iv.from, To: iv.to, Next: iv.next, Remaining: iv.to - iv.next, PendingBatches: len(iv.pending)}, nil
}

type randomOperation struct {
	name string
	args string
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			actual := NewScheduler()
			expected := newNaiveScheduler()
			var log strings.Builder
			for step := 0; step < 60; step++ {
				op := randomOperation{}
				switch rng.Intn(6) {
				case 0:
					from := expected.watermark + int64(rng.Intn(11)) - 2
					to := from + 1 + int64(rng.Intn(8))
					if rng.Intn(20) == 0 {
						to = MaxOffset + int64(rng.Intn(3)) - 1
					}
					op = randomOperation{"Add", fmt.Sprintf("%d,%d", from, to)}
					gotID, gotErr := actual.Add(from, to)
					wantID, wantErr := expected.add(from, to)
					reason := compareError(gotErr, wantErr)
					if gotID.ID != wantID {
						reason = fmt.Sprintf("id got=%d want=%d", gotID.ID, wantID)
					}
					logOp(&log, op, fmt.Sprintf("id=%d,err=%v", gotID.ID, gotErr), fmt.Sprintf("id=%d,err=%v", wantID, wantErr), reason)
					if reason != "match" {
						t.Fatalf("Add mismatch\n%s", log.String())
					}
				case 1:
					id := int64(rng.Intn(int(expected.nextID)+3)) - 1
					n := []Offset{0, 1, 2, 3, 5, 8, MaxBatchSize, MaxBatchSize + 1}[rng.Intn(8)]
					op = randomOperation{"Process", fmt.Sprintf("%d,%d", id, n)}
					got, gotErr := actual.Process(id, n)
					want, wantErr := expected.process(id, n)
					reason := compareResult(got, want, gotErr, wantErr)
					logOp(&log, op, fmt.Sprintf("%+v,err=%v", got, gotErr), fmt.Sprintf("%+v,err=%v", want, wantErr), reason)
					if reason != "match" {
						t.Fatalf("Process mismatch\n%s", log.String())
					}
				case 2:
					id := int64(rng.Intn(int(expected.nextID)+3)) - 1
					den := []Offset{0, 1, 2, 3, 5, MaxDenominator, MaxDenominator + 1}[rng.Intn(7)]
					num := den + int64(rng.Intn(5)) - 2
					if rng.Intn(5) == 0 {
						num = 0
					}
					op = randomOperation{"Split", fmt.Sprintf("%d,%d,%d", id, num, den)}
					got, gotErr := actual.Split(id, num, den)
					want, wantErr := expected.split(id, num, den)
					reason := compareError(gotErr, wantErr)
					if got.ID != want {
						reason = fmt.Sprintf("id got=%d want=%d", got.ID, want)
					}
					logOp(&log, op, fmt.Sprintf("id=%d,err=%v", got.ID, gotErr), fmt.Sprintf("id=%d,err=%v", want, wantErr), reason)
					if reason != "match" {
						t.Fatalf("Split mismatch\n%s", log.String())
					}
				case 3:
					id := int64(rng.Intn(int(expected.nextBatch)+3)) - 1
					op = randomOperation{"Ack", fmt.Sprintf("%d", id)}
					gotErr := actual.Ack(id)
					wantErr := expected.ack(id)
					reason := compareError(gotErr, wantErr)
					logOp(&log, op, fmt.Sprint(gotErr), fmt.Sprint(wantErr), reason)
					if reason != "match" {
						t.Fatalf("Ack mismatch\n%s", log.String())
					}
				case 4:
					id := int64(rng.Intn(int(expected.nextID)+3)) - 1
					op = randomOperation{"Progress", fmt.Sprintf("%d", id)}
					got, gotErr := actual.Progress(id)
					want, wantErr := expected.progress(id)
					reason := compareResult(got, want, gotErr, wantErr)
					logOp(&log, op, fmt.Sprintf("%+v,err=%v", got, gotErr), fmt.Sprintf("%+v,err=%v", want, wantErr), reason)
					if reason != "match" {
						t.Fatalf("Progress mismatch\n%s", log.String())
					}
				default:
					op = randomOperation{"Watermark", ""}
					got := actual.Watermark()
					want := expected.watermark
					reason := "match"
					if got != want {
						reason = fmt.Sprintf("%d != %d", got, want)
					}
					logOp(&log, op, fmt.Sprint(got), fmt.Sprint(want), reason)
					if reason != "match" {
						t.Fatalf("Watermark mismatch\n%s", log.String())
					}
				}
				compareState(t, actual, expected, &log, op)
			}
			t.Logf("seed=%d input/output/reason log:\n%sstate comparison after every operation: match", seed, log.String())
		})
	}
}

func compareError(got, want error) string {
	if !errors.Is(got, want) {
		return fmt.Sprintf("error got=%v want=%v", got, want)
	}
	return "match"
}

func compareResult[T comparable](got, want T, gotErr, wantErr error) string {
	if reason := compareError(gotErr, wantErr); reason != "match" {
		return reason
	}
	if got != want {
		return fmt.Sprintf("result got=%+v want=%+v", got, want)
	}
	return "match"
}

func logOp(log *strings.Builder, op randomOperation, got, want, reason string) {
	fmt.Fprintf(log, "%s(%s) => %s; expected %s; decision=%s\n", op.name, op.args, got, want, reason)
}

func compareState(t *testing.T, actual *Scheduler, expected *naiveScheduler, log *strings.Builder, op randomOperation) {
	t.Helper()
	if got, want := len(actual.intervals), len(expected.intervals); got != want {
		t.Fatalf("after %s interval count=%d want=%d\n%s", op.name, got, want, log.String())
	}
	for id, wantIV := range expected.intervals {
		gotIV := actual.intervals[id]
		if gotIV == nil || gotIV.from != wantIV.from || gotIV.to != wantIV.to || gotIV.next != wantIV.next || gotIV.pending != len(wantIV.pending) {
			t.Fatalf("after %s interval %d got=%+v pending=%d want=%+v\n%s", op.name, id, gotIV, len(wantIV.pending), wantIV, log.String())
		}
	}
	if got, want := len(actual.batches), len(expected.batches); got != want {
		t.Fatalf("after %s batch count=%d want=%d\n%s", op.name, got, want, log.String())
	}
	for id, wantBatch := range expected.batches {
		gotBatch := actual.batches[id]
		if gotBatch == nil || gotBatch.interval != wantBatch.interval || gotBatch.from != wantBatch.from || gotBatch.to != wantBatch.to || gotBatch.acked != wantBatch.acked {
			t.Fatalf("after %s batch %d got=%+v want=%+v\n%s", op.name, id, gotBatch, wantBatch, log.String())
		}
	}
	if actual.watermark != expected.watermark {
		t.Fatalf("after %s W=%d want=%d\n%s", op.name, actual.watermark, expected.watermark, log.String())
	}
}
