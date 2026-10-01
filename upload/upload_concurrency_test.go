package upload

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

// validSize 生成编号 part 的合规大小。
func validSize(part, total int, maxSize int64, rnd *rand.Rand) int64 {
	if part == total {
		return 1 + rnd.Int63n(maxSize)
	}
	return maxSize
}

// TestSerialReplayMatchesNaiveLedger 串行随机操作逐步与朴素账目对账，
// 并以相同序列在第二个登记器重放，验证统计完全相同。
func TestSerialReplayMatchesNaiveLedger(t *testing.T) {
	const total, maxSize, ops = 8, 64, 400
	rnd := rand.New(rand.NewSource(42))

	type op struct {
		kind string
		part int
		size int64
	}
	seq := make([]op, 0, ops)
	for i := 0; i < ops; i++ {
		part := 1 + rnd.Intn(total)
		seq = append(seq, op{kind: "upload", part: part, size: validSize(part, total, maxSize, rnd)})
		if rnd.Intn(10) == 0 {
			seq = append(seq, op{kind: "complete"})
		}
	}

	run := func() Stats {
		r := NewRegistry()
		id, err := r.CreateSession(total, maxSize)
		if err != nil {
			t.Fatalf("CreateSession 失败: %v", err)
		}
		for _, o := range seq {
			if o.kind == "upload" {
				_ = r.UploadPart(id, o.part, o.size)
			} else {
				_ = r.Complete(id)
			}
		}
		st, err := r.Stats(id)
		if err != nil {
			t.Fatalf("Stats 失败: %v", err)
		}
		return st
	}

	// 第一遍：逐步与朴素账目对账。
	r := NewRegistry()
	id, err := r.CreateSession(total, maxSize)
	if err != nil {
		t.Fatalf("CreateSession 失败: %v", err)
	}
	ledger := map[int]int64{}
	uploaded, completed := 0, false
	for i, o := range seq {
		if o.kind == "upload" {
			err := r.UploadPart(id, o.part, o.size)
			if completed {
				if !errors.Is(err, ErrSessionCompleted) {
					t.Fatalf("op#%d 完成后上传应被拒, err=%v", i, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("op#%d 合规上传意外失败: %v", i, err)
			}
			ledger[o.part] = o.size
			uploaded++
		} else if !completed {
			err := r.Complete(id)
			if len(ledger) == total && err != nil {
				t.Fatalf("op#%d 齐全时 Complete 意外失败: %v", i, err)
			}
			if len(ledger) < total && !errors.Is(err, ErrMissingParts) {
				t.Fatalf("op#%d 缺片时应报 ErrMissingParts, err=%v", i, err)
			}
			completed = err == nil
		}
		st, serr := r.Stats(id)
		if serr != nil {
			t.Fatalf("Stats 失败: %v", serr)
		}
		var wantBytes int64
		for _, sz := range ledger {
			wantBytes += sz
		}
		if st.Overwrites != uploaded-len(ledger) || st.Bytes != wantBytes || st.Completed != completed {
			t.Fatalf("op#%d 账目不符: Stats=%+v 朴素账目 uploaded=%d ledger=%v completed=%v",
				i, st, uploaded, ledger, completed)
		}
	}
	first, _ := r.Stats(id)

	// 第二遍：同一操作序列串行重放，统计须完全相同。
	replay := run()
	t.Logf("判定依据: 首遍 Stats=%+v", first)
	t.Logf("输出: 重放 Stats=%+v", replay)
	if !reflect.DeepEqual(first, replay) {
		t.Errorf("串行重放统计不一致: 首遍=%+v 重放=%+v", first, replay)
	}
}

// TestConcurrentUploadComplete 并发上传与完成交错：
// 任意交错后不变量恒成立，且完成成功后不再有成功上传。
func TestConcurrentUploadComplete(t *testing.T) {
	const total, maxSize, workers, rounds = 16, 128, 8, 200
	r := NewRegistry()
	id, err := r.CreateSession(total, maxSize)
	if err != nil {
		t.Fatalf("CreateSession 失败: %v", err)
	}

	var successUploads atomic.Int64
	var completedFlag atomic.Bool
	var lateSuccess atomic.Int64
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(seed))
			for i := 0; i < rounds; i++ {
				part := 1 + rnd.Intn(total)
				size := validSize(part, total, maxSize, rnd)
				err := r.UploadPart(id, part, size)
				switch {
				case err == nil:
					successUploads.Add(1)
					if completedFlag.Load() {
						lateSuccess.Add(1)
					}
				case errors.Is(err, ErrSessionCompleted):
					// 与完成并发：以已完成被拒，合法。
				default:
					t.Errorf("合规上传出现意外错误: %v", err)
				}
			}
		}(int64(w) + 1)
	}

	// 完成与上传并发：缺片则拒绝并重试，直至齐全后完成成功。
	var completeErr error
	for {
		completeErr = r.Complete(id)
		if !errors.Is(completeErr, ErrMissingParts) {
			break
		}
	}
	if completeErr != nil {
		t.Fatalf("Complete 意外失败: %v", completeErr)
	}
	completedFlag.Store(true)
	wg.Wait()

	st, err := r.Stats(id)
	if err != nil {
		t.Fatalf("Stats 失败: %v", err)
	}
	t.Logf("输入: %d 个 worker 各 %d 次上传 + 并发 Complete", workers, rounds)
	t.Logf("输出: Stats=%+v 成功上传数=%d", st, successUploads.Load())

	// 判定依据一：覆盖次数 == 成功上传数 - 已登记的不同编号数。
	if st.Overwrites != int(successUploads.Load())-st.Registered {
		t.Errorf("Overwrites=%d, 期望 %d-%d=%d",
			st.Overwrites, successUploads.Load(), st.Registered,
			int(successUploads.Load())-st.Registered)
	}
	// 判定依据二：字节数 == 当前各片大小之和。
	var sum int64
	for _, sz := range st.PartSizes {
		sum += sz
	}
	if st.Bytes != sum {
		t.Errorf("Bytes=%d, 期望各片大小之和 %d", st.Bytes, sum)
	}
	// 判定依据三：完成成功后不再有上传成功（要么计入完成、要么被拒）。
	if n := lateSuccess.Load(); n != 0 {
		t.Errorf("完成成功后仍有 %d 次上传成功", n)
	}
	if !st.Completed || st.Registered != total {
		t.Errorf("完成后应冻结且齐全: Completed=%v Registered=%d", st.Completed, st.Registered)
	}
}

// TestConcurrentStatsConsistency 并发查询与上传交错时，
// 每次查询快照自身都满足账目不变量。
func TestConcurrentStatsConsistency(t *testing.T) {
	const total, maxSize = 4, 32
	r := NewRegistry()
	id, err := r.CreateSession(total, maxSize)
	if err != nil {
		t.Fatalf("CreateSession 失败: %v", err)
	}
	var stop atomic.Bool
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(seed))
			for !stop.Load() {
				part := 1 + rnd.Intn(total)
				_ = r.UploadPart(id, part, validSize(part, total, maxSize, rnd))
			}
		}(int64(w) + 7)
	}
	for i := 0; i < 2000; i++ {
		st, err := r.Stats(id)
		if err != nil {
			t.Fatalf("Stats 失败: %v", err)
		}
		var sum int64
		for _, sz := range st.PartSizes {
			sum += sz
		}
		if st.Bytes != sum || st.Overwrites != st.Uploaded-st.Registered || st.Registered != len(st.PartSizes) {
			t.Fatalf("查询快照违反不变量: %+v (各片之和=%d)", st, sum)
		}
	}
	stop.Store(true)
	wg.Wait()
	t.Logf("判定依据: 2000 次并发查询快照均满足 Bytes==各片之和 且 Overwrites==Uploaded-Registered")
}
