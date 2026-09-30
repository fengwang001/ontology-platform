package overflow

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// dumpState returns a printable snapshot of every key's state, the
// overflow block set and the block counter, for test logs.
func dumpState(s *Store) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var b strings.Builder
	keys := make([]string, 0, len(s.records))
	for k := range s.records {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rec := s.records[k]
		if rec.Inline {
			fmt.Fprintf(&b, "  key=%q inline len=%d\n", k, len(rec.Value))
		} else {
			fmt.Fprintf(&b, "  key=%q overflow block=%d\n", k, rec.Block)
		}
	}
	nums := make([]int, 0, len(s.blocks))
	for n := range s.blocks {
		nums = append(nums, int(n))
	}
	sort.Ints(nums)
	fmt.Fprintf(&b, "  blocks=%v nextBlock=%d", nums, s.nextBlock)
	return b.String()
}

// snapshot is a deep copy of the whole store state, used to prove that a
// rejected operation changed nothing.
type snapshot struct {
	records   map[string]Record
	blocks    map[uint64][]byte
	nextBlock uint64
}

func takeSnapshot(s *Store) snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := snapshot{
		records:   make(map[string]Record, len(s.records)),
		blocks:    make(map[uint64][]byte, len(s.blocks)),
		nextBlock: s.nextBlock,
	}
	for k, rec := range s.records {
		rec.Value = clone(rec.Value)
		snap.records[k] = rec
	}
	for n, data := range s.blocks {
		snap.blocks[n] = clone(data)
	}
	return snap
}

func (a snapshot) equal(b snapshot) bool {
	if a.nextBlock != b.nextBlock || len(a.records) != len(b.records) || len(a.blocks) != len(b.blocks) {
		return false
	}
	for k, ra := range a.records {
		rb, ok := b.records[k]
		if !ok || ra.Inline != rb.Inline || ra.Block != rb.Block || !bytes.Equal(ra.Value, rb.Value) {
			return false
		}
	}
	for n, da := range a.blocks {
		db, ok := b.blocks[n]
		if !ok || !bytes.Equal(da, db) {
			return false
		}
	}
	return true
}

func mustStore(t *testing.T, threshold, maxBlocks int) *Store {
	t.Helper()
	s, err := NewStore(threshold, maxBlocks)
	if err != nil {
		t.Fatalf("NewStore(%d, %d): %v", threshold, maxBlocks, err)
	}
	return s
}

func TestThresholdBoundary(t *testing.T) {
	const threshold = 8
	s := mustStore(t, threshold, 10)

	exact := []byte("12345678") // len == threshold -> inline
	over := []byte("123456789") // len == threshold+1 -> overflow

	t.Logf("op: Put(%q, len=%d) 判定依据: len <= threshold(%d) 应内联", "exact", len(exact), threshold)
	if err := s.Put("exact", exact); err != nil {
		t.Fatalf("Put exact: %v", err)
	}
	t.Logf("op: Put(%q, len=%d) 判定依据: len > threshold(%d) 应溢出", "over", len(over), threshold)
	if err := s.Put("over", over); err != nil {
		t.Fatalf("Put over: %v", err)
	}
	t.Logf("state after puts:\n%s", dumpState(s))

	if rec := s.records["exact"]; !rec.Inline {
		t.Fatalf("len == threshold must be inline, got %+v", rec)
	}
	rec := s.records["over"]
	if rec.Inline {
		t.Fatalf("len == threshold+1 must overflow, got %+v", rec)
	}
	if _, ok := s.blocks[rec.Block]; !ok {
		t.Fatalf("overflow block %d missing", rec.Block)
	}
	t.Logf("判定依据: exact 内联、over 溢出到块 %d，二者互斥成立", rec.Block)

	// Cross the boundary in both directions.
	t.Logf("op: Put(exact, len=%d) 小值变大值，应转为溢出", len(over))
	if err := s.Put("exact", over); err != nil {
		t.Fatalf("Put exact->overflow: %v", err)
	}
	t.Logf("op: Put(over, len=%d) 大值变小值，应转为内联并回收旧块 %d", len(exact), rec.Block)
	if err := s.Put("over", exact); err != nil {
		t.Fatalf("Put over->inline: %v", err)
	}
	t.Logf("state after boundary crossing:\n%s", dumpState(s))

	if s.records["exact"].Inline {
		t.Fatal("exact should have become overflow")
	}
	if !s.records["over"].Inline {
		t.Fatal("over should have become inline")
	}
	if _, ok := s.blocks[rec.Block]; ok {
		t.Fatalf("old block %d should have been reclaimed", rec.Block)
	}
	if err := s.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	got, _ := s.Get("exact")
	if !bytes.Equal(got, over) {
		t.Fatalf("Get(exact) = %q, want %q", got, over)
	}
	got, _ = s.Get("over")
	if !bytes.Equal(got, exact) {
		t.Fatalf("Get(over) = %q, want %q", got, exact)
	}
	t.Logf("判定依据: 边界两侧转换后引用不变式 Check 通过，取值与原值一致")
}

func TestUpdateReclaimsOldBlock(t *testing.T) {
	s := mustStore(t, 4, 10)

	if err := s.Put("k", []byte("AAAAA")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	first := s.records["k"].Block
	t.Logf("op: Put(k, AAAAA) -> 溢出块 %d\n%s", first, dumpState(s))

	if err := s.Put("k", []byte("BBBBB")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	second := s.records["k"].Block
	t.Logf("op: Put(k, BBBBB) -> 新块 %d，旧块 %d 应已回收\n%s", second, first, dumpState(s))

	if second <= first {
		t.Fatalf("block numbers must be monotonic: first=%d second=%d", first, second)
	}
	if _, ok := s.blocks[first]; ok {
		t.Fatalf("old block %d should have been reclaimed", first)
	}
	if data, ok := s.blocks[second]; !ok || !bytes.Equal(data, []byte("BBBBB")) {
		t.Fatalf("new block %d = %q, %v; want BBBBB", second, data, ok)
	}
	t.Logf("判定依据: 新块号 %d > 旧块号 %d（单调递增且旧号不复用），旧块已回收，新块内容正确", second, first)

	// The reclaimed number is never reused by later allocations.
	if err := s.Put("k2", []byte("CCCCC")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	third := s.records["k2"].Block
	if third <= second {
		t.Fatalf("reused block number: third=%d after second=%d", third, second)
	}
	t.Logf("op: Put(k2, CCCCC) -> 块 %d，判定依据: 已回收的块号 %d 未被复用", third, first)

	if err := s.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestRecoverReclaimsOrphans(t *testing.T) {
	s := mustStore(t, 4, 10)
	if err := s.Put("k", []byte("AAAAA")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	live := s.records["k"].Block

	// Inject orphan blocks directly, as a crash between "write new block"
	// and "update reference" would leave them.
	s.mu.Lock()
	s.blocks[s.nextBlock] = []byte("orphan-1")
	s.nextBlock++
	s.blocks[s.nextBlock] = []byte("orphan-2")
	s.nextBlock++
	s.mu.Unlock()
	t.Logf("注入孤儿块后（模拟崩溃遗留）:\n%s", dumpState(s))

	rep := s.Recover()
	t.Logf("op: Recover() -> 回收孤儿块 %v，悬挂键 %v", rep.ReclaimedOrphans, rep.DanglingKeys)

	if len(rep.DanglingKeys) != 0 {
		t.Fatalf("unexpected dangling keys: %v", rep.DanglingKeys)
	}
	if len(rep.ReclaimedOrphans) != 2 {
		t.Fatalf("reclaimed %v, want 2 orphans", rep.ReclaimedOrphans)
	}
	for _, n := range rep.ReclaimedOrphans {
		if n == live {
			t.Fatalf("live block %d must not be reclaimed", live)
		}
		if _, ok := s.blocks[n]; ok {
			t.Fatalf("orphan block %d still present after Recover", n)
		}
	}
	if _, ok := s.blocks[live]; !ok {
		t.Fatalf("live block %d missing after Recover", live)
	}
	if got, ok := s.Get("k"); !ok || !bytes.Equal(got, []byte("AAAAA")) {
		t.Fatalf("Get(k) = %q, %v; want AAAAA", got, ok)
	}
	if err := s.Check(); err != nil {
		t.Fatalf("Check after Recover: %v", err)
	}
	t.Logf("判定依据: 无引用孤儿块被回收，存活块 %d 及其引用不受影响，Check 通过\n%s", live, dumpState(s))
}

func TestRecoverDetectsDangling(t *testing.T) {
	s := mustStore(t, 4, 10)
	if err := s.Put("victim", []byte("AAAAA")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put("survivor", []byte("BBBBB")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	victim := s.records["victim"].Block

	// Inject a dangling reference: the block disappears while the main
	// record still points at it.
	s.mu.Lock()
	delete(s.blocks, victim)
	s.mu.Unlock()
	t.Logf("注入悬挂引用后（victim -> 缺失块 %d）:\n%s", victim, dumpState(s))

	rep := s.Recover()
	t.Logf("op: Recover() -> 回收孤儿块 %v，检出悬挂键 %v", rep.ReclaimedOrphans, rep.DanglingKeys)

	if len(rep.DanglingKeys) != 1 || rep.DanglingKeys[0] != "victim" {
		t.Fatalf("DanglingKeys = %v, want [victim]", rep.DanglingKeys)
	}
	if err := s.Check(); err == nil {
		t.Fatal("Check must report the dangling reference")
	} else {
		t.Logf("判定依据: Check 报错 %q，悬挂引用可被自检检出", err)
	}
	if got, ok := s.Get("survivor"); !ok || !bytes.Equal(got, []byte("BBBBB")) {
		t.Fatalf("Get(survivor) = %q, %v; want BBBBB", got, ok)
	}
}

// TestCrashBetweenStepsNeverDangles simulates a crash after every step of
// the update ordering (write new block -> update reference -> reclaim old
// block) and proves no crash point can leave a reference to a missing
// block, and that recovery always restores the invariants.
func TestCrashBetweenStepsNeverDangles(t *testing.T) {
	s := mustStore(t, 4, 10)
	if err := s.Put("k", []byte("AAAAA")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	oldBlock := s.records["k"].Block
	t.Logf("setup: k -> 块 %d（值 AAAAA）", oldBlock)

	// Crash point 1: after writing the new block, before updating the
	// reference. The new block is an orphan; the reference is intact.
	s.mu.Lock()
	newBlock := s.nextBlock
	s.blocks[newBlock] = []byte("BBBBB")
	s.nextBlock++
	s.mu.Unlock()
	t.Logf("崩溃点1: 新块 %d 已写入、引用未更新\n%s", newBlock, dumpState(s))

	if got, ok := s.Get("k"); !ok || !bytes.Equal(got, []byte("AAAAA")) {
		t.Fatalf("after crash point 1, Get(k) = %q, %v; want AAAAA", got, ok)
	}
	rep := s.Recover()
	if len(rep.DanglingKeys) != 0 || len(rep.ReclaimedOrphans) != 1 || rep.ReclaimedOrphans[0] != newBlock {
		t.Fatalf("Recover after crash point 1 = %+v, want reclaim [%d] only", rep, newBlock)
	}
	t.Logf("判定依据: 崩溃点1 引用仍指向存在的旧块 %d，孤儿新块 %d 被回收", oldBlock, newBlock)

	// Crash point 2: after updating the reference, before reclaiming the
	// old block. The old block is an orphan; the reference is intact.
	s.mu.Lock()
	newBlock2 := s.nextBlock
	s.blocks[newBlock2] = []byte("CCCCC")
	s.nextBlock++
	s.records["k"] = Record{Inline: false, Block: newBlock2}
	s.mu.Unlock()
	t.Logf("崩溃点2: 引用已更新到块 %d、旧块 %d 未回收\n%s", newBlock2, oldBlock, dumpState(s))

	if got, ok := s.Get("k"); !ok || !bytes.Equal(got, []byte("CCCCC")) {
		t.Fatalf("after crash point 2, Get(k) = %q, %v; want CCCCC", got, ok)
	}
	rep = s.Recover()
	if len(rep.DanglingKeys) != 0 || len(rep.ReclaimedOrphans) != 1 || rep.ReclaimedOrphans[0] != oldBlock {
		t.Fatalf("Recover after crash point 2 = %+v, want reclaim [%d] only", rep, oldBlock)
	}
	t.Logf("判定依据: 崩溃点2 引用指向已存在的新块 %d，孤儿旧块 %d 被回收", newBlock2, oldBlock)

	if err := s.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	t.Logf("最终状态（无悬挂、无孤儿）:\n%s", dumpState(s))
}

func TestDeleteReclaimsBlock(t *testing.T) {
	s := mustStore(t, 4, 10)

	if err := s.Put("big", []byte("12345")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	num := s.records["big"].Block
	t.Logf("op: Put(big, 12345) -> 溢出块 %d", num)

	if err := s.Delete("big"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	t.Logf("op: Delete(big)\n%s", dumpState(s))

	if _, ok := s.blocks[num]; ok {
		t.Fatalf("block %d should have been reclaimed on delete", num)
	}
	if _, ok := s.Get("big"); ok {
		t.Fatal("Get after delete should miss")
	}
	if err := s.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	t.Logf("判定依据: 删除后溢出块集合为空、主记录移除、Check 通过")
}

func TestFailureIsAtomic(t *testing.T) {
	if _, err := NewStore(0, 1); !errors.Is(err, ErrInvalidThreshold) {
		t.Fatalf("NewStore(0,1) err = %v, want ErrInvalidThreshold", err)
	}
	if _, err := NewStore(-1, 1); !errors.Is(err, ErrInvalidThreshold) {
		t.Fatalf("NewStore(-1,1) err = %v, want ErrInvalidThreshold", err)
	}
	if _, err := NewStore(1, 0); !errors.Is(err, ErrInvalidMaxBlocks) {
		t.Fatalf("NewStore(1,0) err = %v, want ErrInvalidMaxBlocks", err)
	}
	t.Logf("判定依据: 非法阈值/块数上限分别返回 ErrInvalidThreshold/ErrInvalidMaxBlocks，可区分")

	s := mustStore(t, 4, 2)
	if err := s.Put("a", []byte("AAAAA")); err != nil {
		t.Fatalf("Put a: %v", err)
	}
	if err := s.Put("b", []byte("BBBBB")); err != nil {
		t.Fatalf("Put b: %v", err)
	}
	t.Logf("setup: a->块1 b->块2，块数已达上限 2\n%s", dumpState(s))

	before := takeSnapshot(s)

	if err := s.Put("c", []byte("CCCCC")); !errors.Is(err, ErrTooManyBlocks) {
		t.Fatalf("Put over limit err = %v, want ErrTooManyBlocks", err)
	}
	t.Logf("op: Put(c, CCCCC) 被拒绝: ErrTooManyBlocks（溢出块数超限）")
	if err := s.Put("", []byte("x")); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Put empty key err = %v, want ErrEmptyKey", err)
	}
	t.Logf("op: Put(\"\", x) 被拒绝: ErrEmptyKey")
	if err := s.Delete(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Delete empty key err = %v, want ErrEmptyKey", err)
	}
	if err := s.Delete("missing"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Delete missing err = %v, want ErrKeyNotFound", err)
	}
	t.Logf("op: Delete(\"\")/Delete(missing) 被拒绝: ErrEmptyKey/ErrKeyNotFound")

	after := takeSnapshot(s)
	if !before.equal(after) {
		t.Fatalf("failed ops mutated state:\n%s", dumpState(s))
	}
	t.Logf("判定依据: 失败前后快照逐字段一致（主记录、溢出表、块号计数均未变）\n%s", dumpState(s))

	if err := s.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

// TestConcurrentReadsIdentical hammers Get/Block/Check from many
// goroutines while no writes happen: every read of the same key must
// return field-identical bytes and Check must never fail.
func TestConcurrentReadsIdentical(t *testing.T) {
	s := mustStore(t, 8, 64)
	want := map[string][]byte{
		"inline-1": []byte("tiny"),
		"inline-2": []byte("12345678"),
		"big-1":    bytes.Repeat([]byte("x"), 100),
		"big-2":    bytes.Repeat([]byte("y"), 257),
	}
	for k, v := range want {
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put(%q): %v", k, err)
		}
	}
	t.Logf("setup: 2 个内联键 + 2 个溢出键\n%s", dumpState(s))

	const readers = 8
	const rounds = 200
	var wg sync.WaitGroup
	errs := make(chan error, readers*rounds)
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				for k, v := range want {
					got, ok := s.Get(k)
					if !ok || !bytes.Equal(got, v) {
						errs <- fmt.Errorf("reader %d: Get(%q) mismatch", id, k)
						return
					}
				}
				if err := s.Check(); err != nil {
					errs <- fmt.Errorf("reader %d: Check: %w", id, err)
					return
				}
			}
		}(r)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	t.Logf("判定依据: %d 个并发读协程 x %d 轮，所有 Get 逐字段相同、Check 全部通过", readers, rounds)
}

// TestConcurrentReadWriteInvariants runs writers and readers together:
// at any moment every reference must point to an existing block and every
// block must be referenced by exactly one record.
func TestConcurrentReadWriteInvariants(t *testing.T) {
	s := mustStore(t, 8, 32)
	keys := []string{"a", "b", "c", "d"}

	const writers = 4
	const readers = 4
	const ops = 500

	var writerWG, readerWG sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan error, readers*ops)

	for w := 0; w < writers; w++ {
		writerWG.Add(1)
		go func(id int) {
			defer writerWG.Done()
			rng := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < ops; i++ {
				k := keys[rng.Intn(len(keys))]
				v := make([]byte, rng.Intn(40))
				rng.Read(v)
				if rng.Intn(4) == 0 {
					_ = s.Delete(k)
				} else if err := s.Put(k, v); err != nil && !errors.Is(err, ErrTooManyBlocks) {
					errs <- fmt.Errorf("writer %d: Put: %w", id, err)
				}
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		readerWG.Add(1)
		go func(id int) {
			defer readerWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := s.Check(); err != nil {
					errs <- fmt.Errorf("reader %d: Check: %w", id, err)
					return
				}
				for _, k := range keys {
					s.Get(k)
				}
			}
		}(r)
	}

	writerWG.Wait()
	close(stop)
	readerWG.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if err := s.Check(); err != nil {
		t.Fatalf("final Check: %v", err)
	}
	t.Logf("判定依据: %d 写 x %d 读并发 %d 次操作期间 Check 从未失败，最终状态:\n%s", writers, readers, ops, dumpState(s))
}

// TestNaiveReplay applies a deterministic pseudo-random operation
// sequence to both the store and a naive map model, then compares every
// key's value. The seed is fixed so the run is fully reproducible.
func TestNaiveReplay(t *testing.T) {
	const (
		threshold = 16
		maxBlocks = 64
		ops       = 500
		seed      = 42
	)
	s := mustStore(t, threshold, maxBlocks)
	model := make(map[string][]byte)
	keys := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	rng := rand.New(rand.NewSource(seed))

	for i := 0; i < ops; i++ {
		k := keys[rng.Intn(len(keys))]
		switch rng.Intn(3) {
		case 0, 1: // Put with a random-length value crossing the threshold
			v := make([]byte, rng.Intn(3*threshold))
			rng.Read(v)
			if err := s.Put(k, v); err != nil {
				t.Fatalf("op %d: Put(%q, len=%d): %v", i, k, len(v), err)
			}
			model[k] = v
			if i%100 == 0 {
				t.Logf("op %d: Put(%q, len=%d)", i, k, len(v))
			}
		case 2: // Delete
			_, existed := model[k]
			err := s.Delete(k)
			if existed && err != nil {
				t.Fatalf("op %d: Delete(%q): %v", i, k, err)
			}
			if !existed && !errors.Is(err, ErrKeyNotFound) {
				t.Fatalf("op %d: Delete(%q) err = %v, want ErrKeyNotFound", i, k, err)
			}
			delete(model, k)
			if i%100 == 0 {
				t.Logf("op %d: Delete(%q) existed=%v", i, k, existed)
			}
		}
		if err := s.Check(); err != nil {
			t.Fatalf("op %d: invariant broken: %v", i, err)
		}
	}

	t.Logf("重放 %d 次操作后状态:\n%s", ops, dumpState(s))

	// Every model key must match; the store must not have extra keys.
	for k, want := range model {
		got, ok := s.Get(k)
		if !ok || !bytes.Equal(got, want) {
			t.Fatalf("Get(%q) = %q, %v; want %q", k, got, ok, want)
		}
	}
	s.mu.RLock()
	storeKeys := len(s.records)
	s.mu.RUnlock()
	if storeKeys != len(model) {
		t.Fatalf("store has %d keys, model has %d", storeKeys, len(model))
	}
	t.Logf("判定依据: 固定种子 %d 重放 %d 次操作，存储与朴素 map 模型逐键逐字段一致（%d 个键）", seed, ops, len(model))
}
