package changelog

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// readObservation 记录一次并发读到的结果。
type readObservation struct {
	gen       int64
	key       string
	pos       int64
	seq       int64
	value     string
	compacted bool
	exists    bool
}

// expectedAtGen 在记录快照 refs[gen] 上按位点语义计算期望值。
// refs[0] 为初始快照，gen 与 Log.Generation() 一一对应。
func expectedAtGen(refs [][]Record, gen int64, key string, pos int64) (Record, bool) {
	var best Record
	found := false
	for _, rec := range refs[gen] {
		if rec.Seq <= pos && rec.Key == key {
			best, found = rec, true
		}
	}
	return best, found
}

// TestConcurrentReadsDuringAppendAndCompact 验证：
//  1. ReadAt/Verify 可与彼此及 Append/Compact 并发（-race 下无数据竞争）；
//  2. 读结果与“某一代串行参照”逐值相同，即读永远落在某个已提交的一致状态上；
//  3. 压缩期间读不报错（合法位点），压缩后所有位点仍可寻址；
//  4. 最终状态与把同一操作流串行重放的参照日志逐记录相同。
func TestConcurrentReadsDuringAppendAndCompact(t *testing.T) {
	const iterations = 200

	// 预填基础数据，保证读者一开始就有合法位点。
	actual := New()
	reference := New()
	for _, w := range []struct{ key, value string }{
		{"a", "seed-a"}, {"b", "seed-b"}, {"a", "seed-a2"},
	} {
		if _, err := actual.Append(w.key, w.value); err != nil {
			t.Fatal(err)
		}
		if _, err := reference.Append(w.key, w.value); err != nil {
			t.Fatal(err)
		}
	}

	// 串行参照以代数为键保存每次成功变更后的不可变记录快照。
	// 索引规则：refs[g] 与 actual.Generation()==g 严格对应。
	// 快照在 actual 提交之前发布（不可变切片整体替换原子指针）：
	// 读者一旦看到 actual 第 g 代，refs[g] 必然已经存在；
	// 读者看到更早代数时索引旧快照，也不会读到未来状态。
	var refsPtr atomic.Pointer[[][]Record]
	// 索引必须等于代数：预填的种子写入已把代数推进到 seedGen，
	// 因此把种子状态填充到 0..seedGen（0..seedGen-1 不可达，仅占位）。
	seedGen := actual.Generation()
	initial := make([][]Record, seedGen+1)
	for i := range initial {
		initial[i] = reference.Records()
	}
	refsPtr.Store(&initial)
	publishRef := func() {
		old := *refsPtr.Load()
		next := make([][]Record, len(old)+1)
		copy(next, old)
		next[len(old)] = reference.Records()
		refsPtr.Store(&next)
	}

	// 操作流由单个 mutator goroutine 串行提交，actual 与 reference
	// 在同一临界区内先后执行，保证 reference 的第 g 代即 actual 第 g 代的参照。
	var opMu sync.Mutex
	applyAppend := func(i int) {
		opMu.Lock()
		defer opMu.Unlock()
		key := []string{"a", "b", "c", "d"}[i%4]
		value := fmt.Sprintf("%s-v%d", key, i)
		if _, err := reference.Append(key, value); err != nil {
			t.Errorf("reference append: %v", err)
			return
		}
		// 参照快照先于 actual 提交发布：读者看到 actual 第 g 代时 refs[g] 已存在。
		publishRef()
		if _, err := actual.Append(key, value); err != nil {
			t.Errorf("actual append: %v", err)
			return
		}
	}
	applyCompact := func(i int) {
		opMu.Lock()
		defer opMu.Unlock()
		last := reference.NextSeq() - 1
		left := int64(1 + i%3)
		right := last
		if left > right {
			left = right
		}
		r2, err := reference.Compact(left, right)
		if err != nil {
			t.Errorf("reference compact: %v", err)
			return
		}
		publishRef()
		r1, err := actual.Compact(left, right)
		if err != nil {
			t.Errorf("actual compact: %v", err)
			return
		}
		if r1 != r2 {
			t.Errorf("compact result diverge: actual=%+v reference=%+v", r1, r2)
		}
	}

	var observations atomic.Int64
	var readErrors atomic.Int64
	var verifyErrors atomic.Int64

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 多个读者：读时记录实际代数，稍后与该代参照快照逐值核对。
	readerCount := runtime.GOMAXPROCS(0)
	if readerCount > 4 {
		readerCount = 4
	}
	obsCh := make(chan readObservation, iterations*readerCount*2)
	for r := 0; r < readerCount; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				runtime.Gosched()
				// 一次性取不可变快照：读取与代数标注必须来自同一次原子加载，
				// 否则读结果与参照代数可能跨越一次提交。
				snap := actual.state.Load()
				gen := snap.generation
				nextSeq := snap.nextSeq
				if nextSeq <= 1 {
					continue
				}
				key := []string{"a", "b", "c", "d", "missing"}[id%5]
				pos := int64(int(1) + int((gen+int64(id))%(nextSeq-1)))
				if key == "" || pos < 1 || pos >= nextSeq {
					continue
				}
				rec, exists := readSnapshot(snap, key, pos)
				// 缓冲满则丢弃该观测，避免读者阻塞在发送上导致死锁。
				select {
				case obsCh <- readObservation{
					gen: gen, key: key, pos: pos,
					seq: rec.Seq, value: rec.Value, compacted: rec.Compacted, exists: exists,
				}:
					observations.Add(1)
				default:
				}
			}
		}(r)
	}

	// 自检者：Verify 与所有操作并发，永远只应通过或报告结构损坏。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			runtime.Gosched()
			if _, err := actual.Verify(); err != nil {
				verifyErrors.Add(1)
				t.Errorf("concurrent Verify: %v", err)
			}
		}
	}()

	// mutator：追加与压缩交替进行，读者在整个期间持续读取。
	for i := 0; i < iterations; i++ {
		if i%2 == 0 {
			applyAppend(i)
		} else {
			applyCompact(i)
		}
		if i%17 == 0 {
			runtime.Gosched()
		}
	}
	close(stop)
	wg.Wait()
	close(obsCh)

	stepf(t, "并发输入: %d 次串行变更(追加/压缩交替), %d 个并发读者+1个并发自检",
		iterations, readerCount)
	stepf(t, "并发读返回观测数=%d, 读错误=%d, 自检错误=%d 判定依据: 读代数对应的串行参照",
		observations.Load(), readErrors.Load(), verifyErrors.Load())
	if observations.Load() == 0 {
		t.Fatal("没有采集到任何并发读观测")
	}

	// 逐观测与同代参照核对（读只可能看到它读到 gen 时已提交的状态）。
	refs := *refsPtr.Load()
	checked := 0
	for obs := range obsCh {
		gen := obs.gen
		if gen < 0 || int(gen) >= len(refs) {
			t.Fatalf("观测代数 %d 超出参照代数范围 %d", gen, len(refs)-1)
		}
		expRec, expExists := expectedAtGen(refs, gen, obs.key, obs.pos)
		if obs.exists != expExists {
			t.Fatalf("gen=%d ReadAt(%q,%d) exists=%v 参照=%v",
				gen, obs.key, obs.pos, obs.exists, expExists)
		}
		if obs.exists {
			if obs.seq != expRec.Seq || obs.value != expRec.Value || obs.compacted != expRec.Compacted {
				t.Fatalf("gen=%d ReadAt(%q,%d) 实际={%d %q compacted=%v} 参照=%+v",
					gen, obs.key, obs.pos, obs.seq, obs.value, obs.compacted, expRec)
			}
		}
		checked++
	}
	stepf(t, "逐值核对完成: %d 条观测全部与同代串行参照相同", checked)

	// 最终状态必须与串行重放完全一致。
	finalActual := actual.Records()
	finalRef := reference.Records()
	if len(finalActual) != len(finalRef) {
		t.Fatalf("最终条目数 actual=%d reference=%d", len(finalActual), len(finalRef))
	}
	for i := range finalRef {
		if finalActual[i] != finalRef[i] {
			t.Fatalf("最终第%d条 actual=%+v reference=%+v", i, finalActual[i], finalRef[i])
		}
	}
	if actual.NextSeq() != reference.NextSeq() {
		t.Fatalf("NextSeq 不一致 actual=%d reference=%d", actual.NextSeq(), reference.NextSeq())
	}
	stepf(t, "最终状态判定: %d 条记录与 NextSeq=%d 与串行参照完全一致",
		len(finalActual), actual.NextSeq())

	// 压缩结束后任意合法位点仍可寻址，且与最终参照一致。
	for pos := int64(1); pos < actual.NextSeq(); pos++ {
		for _, key := range []string{"a", "b", "c", "d", "missing"} {
			rec, exists, err := actual.ReadAt(key, pos)
			if err != nil {
				t.Fatalf("最终 ReadAt(%q,%d) 错误: %v", key, pos, err)
			}
			expRec, expExists := expectedAtGen(refs, int64(len(refs)-1), key, pos)
			if exists != expExists || (exists && rec != expRec) {
				t.Fatalf("最终 ReadAt(%q,%d) actual={%v %v} reference={%v %v}",
					key, pos, exists, rec, expExists, expRec)
			}
		}
	}
	stepf(t, "最终判定: 位点 1..%d 全部可寻址并与参照逐值相同", actual.NextSeq()-1)
}
