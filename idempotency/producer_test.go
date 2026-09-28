package idempotency

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func req(pid string, epoch int64, part int32, seq int64) Request {
	return Request{ProducerID: pid, Epoch: epoch, Partition: part, Sequence: seq, Payload: []byte("x")}
}

func mustAppend(t *testing.T, v *Validator, r Request, wantOffset int64) {
	t.Helper()
	res, err := v.Append(r)
	if err != nil {
		t.Fatalf("Append(%+v) unexpected error: %v", r, err)
	}
	if res.Duplicate {
		t.Fatalf("Append(%+v) unexpected duplicate result", r)
	}
	if res.Offset != wantOffset {
		t.Fatalf("Append(%+v) offset=%d, want %d", r, res.Offset, wantOffset)
	}
}

func mustReject(t *testing.T, v *Validator, r Request, want error) {
	t.Helper()
	if _, err := v.Append(r); !errors.Is(err, want) {
		t.Fatalf("Append(%+v) err=%v, want %v", r, err, want)
	}
}

func TestAppendContiguousOffsets(t *testing.T) {
	v := NewValidator(4)
	for i := int64(0); i < 6; i++ {
		mustAppend(t, v, req("p1", 0, 0, i), i)
	}
	// 不同分区各自维护独立的位点序列。
	mustAppend(t, v, req("p1", 0, 1, 0), 0)
	mustAppend(t, v, req("p1", 0, 1, 1), 1)
}

func TestDuplicateInWindowReturnsOriginalOffset(t *testing.T) {
	v := NewValidator(4)
	for i := int64(0); i < 3; i++ {
		mustAppend(t, v, req("p1", 0, 0, i), i)
	}
	res, err := v.Append(req("p1", 0, 0, 1))
	if err != nil {
		t.Fatalf("duplicate append error: %v", err)
	}
	if !res.Duplicate || res.Offset != 1 {
		t.Fatalf("duplicate result=%+v, want offset=1 duplicate=true", res)
	}
	// 重复确认不追加日志：下一条仍落在位点 3。
	mustAppend(t, v, req("p1", 0, 0, 3), 3)
}

func TestSequenceGapRejected(t *testing.T) {
	v := NewValidator(4)
	mustAppend(t, v, req("p1", 0, 0, 0), 0)
	mustReject(t, v, req("p1", 0, 0, 2), ErrOutOfOrderSequence)
	// 新分区首条非零同样按跳号拒绝。
	mustReject(t, v, req("p1", 0, 7, 3), ErrOutOfOrderSequence)
}

func TestStaleDuplicateEvictedFromWindow(t *testing.T) {
	v := NewValidator(2)
	for i := int64(0); i < 3; i++ {
		mustAppend(t, v, req("p1", 0, 0, i), i)
	}
	// 窗口只保留 {1,2}，序号 0 已滑出。
	mustReject(t, v, req("p1", 0, 0, 0), ErrDuplicateSequenceStale)
	// 窗口内的 2 仍是重复确认。
	res, err := v.Append(req("p1", 0, 0, 2))
	if err != nil || !res.Duplicate || res.Offset != 2 {
		t.Fatalf("res=%+v err=%v, want duplicate offset=2", res, err)
	}
}

func TestEpochBumpResetsAndFences(t *testing.T) {
	v := NewValidator(4)
	for i := int64(0); i < 3; i++ {
		mustAppend(t, v, req("p1", 0, 0, i), i)
	}
	// 世代升级后首条必须为零。
	mustReject(t, v, req("p1", 1, 0, 3), ErrOutOfOrderSequence)
	// 升级拒绝不得改变世代：旧世代仍应被接受而非被围栏。
	mustAppend(t, v, req("p1", 0, 0, 3), 3)

	mustAppend(t, v, req("p1", 1, 0, 0), 0)
	// 旧世代被围栏。
	mustReject(t, v, req("p1", 0, 0, 4), ErrProducerFenced)
	// 新世代下序号空间重新从零开始，位点也重新计数。
	mustAppend(t, v, req("p1", 1, 0, 1), 1)
	// 世代升级清空窗口：旧世代的序号 0 不再是重复，而是新世代已接受。
	res, err := v.Append(req("p1", 1, 0, 0))
	if err != nil || !res.Duplicate || res.Offset != 0 {
		t.Fatalf("res=%+v err=%v, want duplicate offset=0", res, err)
	}
}

func TestInvalidRequestsRejected(t *testing.T) {
	v := NewValidator(4)
	cases := []Request{
		{ProducerID: "", Epoch: 0, Partition: 0, Sequence: 0, Payload: []byte("x")},
		{ProducerID: "p1", Epoch: -1, Partition: 0, Sequence: 0, Payload: []byte("x")},
		{ProducerID: "p1", Epoch: 0, Partition: -1, Sequence: 0, Payload: []byte("x")},
		{ProducerID: "p1", Epoch: 0, Partition: 0, Sequence: -1, Payload: []byte("x")},
		{ProducerID: "p1", Epoch: 0, Partition: 0, Sequence: 0, Payload: nil},
	}
	for _, c := range cases {
		mustReject(t, v, c, ErrInvalidRequest)
	}
	// 非法请求不留痕：合法首条仍从零开始。
	mustAppend(t, v, req("p1", 0, 0, 0), 0)
}

func TestRejectionLeavesStateUntouched(t *testing.T) {
	v := NewValidator(2)
	for i := int64(0); i < 3; i++ {
		mustAppend(t, v, req("p1", 0, 0, i), i)
	}
	// 各类拒绝。
	mustReject(t, v, req("p1", 0, 0, 9), ErrOutOfOrderSequence)
	mustReject(t, v, req("p1", 0, 0, 0), ErrDuplicateSequenceStale)
	mustReject(t, v, Request{ProducerID: "p1", Epoch: 0, Partition: 0, Sequence: 3}, ErrInvalidRequest)
	mustAppend(t, v, req("p1", 2, 0, 0), 0)
	mustReject(t, v, req("p1", 1, 0, 0), ErrProducerFenced)
	mustReject(t, v, req("p1", 0, 0, 0), ErrProducerFenced)

	// 状态不变：世代仍为 2，下一序号为 1，窗口仍含 0。
	mustAppend(t, v, req("p1", 2, 0, 1), 1)
	res, err := v.Append(req("p1", 2, 0, 0))
	if err != nil || !res.Duplicate || res.Offset != 0 {
		t.Fatalf("res=%+v err=%v, want duplicate offset=0", res, err)
	}
}

func TestConcurrentDuplicateAppendOnce(t *testing.T) {
	const (
		sequences = 64
		workers   = 8
	)
	v := NewValidator(sequences)
	for seq := int64(0); seq < sequences; seq++ {
		results := make(chan Result, workers)
		errs := make(chan error, workers)
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := v.Append(req("p1", 0, 0, seq))
				if err != nil {
					errs <- err
					return
				}
				results <- res
			}()
		}
		wg.Wait()
		close(results)
		close(errs)
		for err := range errs {
			t.Fatalf("seq=%d unexpected error: %v", seq, err)
		}
		appends, duplicates := 0, 0
		for res := range results {
			if res.Offset != seq {
				t.Fatalf("seq=%d got offset=%d", seq, res.Offset)
			}
			if res.Duplicate {
				duplicates++
			} else {
				appends++
			}
		}
		if appends != 1 || duplicates != workers-1 {
			t.Fatalf("seq=%d appends=%d duplicates=%d, want 1/%d", seq, appends, duplicates, workers-1)
		}
	}
	// 日志恰好 sequences 条，位点连续。
	mustAppend(t, v, req("p1", 0, 0, sequences), sequences)
}

// reference 是朴素参照实现：保留完整接受历史，直接按规格判定。
type reference struct {
	windowSize int
	epochs     map[string]int64
	accepted   map[partKey][]int64 // 当前世代下按序接受的序号
}

func newReference(windowSize int) *reference {
	return &reference{
		windowSize: windowSize,
		epochs:     make(map[string]int64),
		accepted:   make(map[partKey][]int64),
	}
}

func (r *reference) append(q Request) (Result, error) {
	if q.ProducerID == "" || q.Epoch < 0 || q.Partition < 0 || q.Sequence < 0 || q.Payload == nil {
		return Result{}, ErrInvalidRequest
	}
	cur, seen := r.epochs[q.ProducerID]
	if seen && q.Epoch < cur {
		return Result{}, ErrProducerFenced
	}
	bump := !seen || q.Epoch > cur
	key := partKey{producer: q.ProducerID, partition: q.Partition}
	seqs := r.accepted[key]
	if bump || len(seqs) == 0 {
		if q.Sequence != 0 {
			return Result{}, ErrOutOfOrderSequence
		}
		if bump {
			r.epochs[q.ProducerID] = q.Epoch
			for key := range r.accepted {
				if key.producer == q.ProducerID {
					delete(r.accepted, key)
				}
			}
		}
		r.accepted[key] = []int64{0}
		return Result{Offset: 0}, nil
	}
	last := seqs[len(seqs)-1]
	if q.Sequence == last+1 {
		r.accepted[key] = append(seqs, q.Sequence)
		return Result{Offset: last + 1}, nil
	}
	if q.Sequence > last+1 {
		return Result{}, ErrOutOfOrderSequence
	}
	from := len(seqs) - r.windowSize
	if from < 0 {
		from = 0
	}
	for _, s := range seqs[from:] {
		if s == q.Sequence {
			return Result{Offset: q.Sequence, Duplicate: true}, nil
		}
	}
	return Result{}, ErrDuplicateSequenceStale
}

func TestMatchesNaiveReference(t *testing.T) {
	rng := rand.New(rand.NewSource(311))
	const window = 3
	v := NewValidator(window)
	ref := newReference(window)
	producers := []string{"a", "b", ""}

	for step := 0; step < 5000; step++ {
		q := Request{
			ProducerID: producers[rng.Intn(len(producers))],
			Epoch:      int64(rng.Intn(4)),
			Partition:  int32(rng.Intn(3)),
			Sequence:   int64(rng.Intn(8)),
			Payload:    []byte("x"),
		}
		if rng.Intn(20) == 0 {
			q.Payload = nil
		}
		gotRes, gotErr := v.Append(q)
		wantRes, wantErr := ref.append(q)
		if !errors.Is(gotErr, wantErr) {
			t.Fatalf("step=%d req=%+v err=%v, want %v", step, q, gotErr, wantErr)
		}
		if gotErr == nil && gotRes != wantRes {
			t.Fatalf("step=%d req=%+v res=%+v, want %+v", step, q, gotRes, wantRes)
		}
	}
}

func ExampleValidator_Append() {
	v := NewValidator(8)
	res, _ := v.Append(req("p1", 0, 0, 0))
	fmt.Println(res.Offset, res.Duplicate)
	res, _ = v.Append(req("p1", 0, 0, 0))
	fmt.Println(res.Offset, res.Duplicate)
	// Output:
	// 0 false
	// 0 true
}
