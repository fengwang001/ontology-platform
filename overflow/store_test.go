package overflow

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func openStore(t *testing.T, threshold, maxBlocks int) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(Config{Dir: dir, Threshold: threshold, MaxBlocks: maxBlocks})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s, dir
}

func reopen(t *testing.T, dir string, threshold, maxBlocks int) *Store {
	t.Helper()
	s, err := Open(Config{Dir: dir, Threshold: threshold, MaxBlocks: maxBlocks})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	return s
}

// logState 打印各键状态、溢出块集合与判定依据。
func logState(t *testing.T, s *Store, note string) {
	t.Helper()
	t.Logf("%s: overflow blocks = %v", note, s.OverflowBlocks())
	for _, k := range s.Keys() {
		v, _ := s.Get(k)
		if blk, ok := s.BlockOf(k); ok {
			t.Logf("  key=%q mode=overflow block=%d len=%d", k, blk, len(v))
		} else {
			t.Logf("  key=%q mode=inline   len=%d", k, len(v))
		}
	}
	if err := s.Verify(); err != nil {
		t.Logf("  verify: %v", err)
	} else {
		t.Logf("  verify: ok (every ref points to an existing block; every block referenced exactly once)")
	}
}

func mkval(seed byte, n int) []byte {
	v := make([]byte, n)
	for i := range v {
		v[i] = seed + byte(i%7)
	}
	return v
}

func TestThresholdBoundary(t *testing.T) {
	const threshold = 8
	s, _ := openStore(t, threshold, 16)

	at := mkval('a', threshold)
	over := mkval('b', threshold+1)
	t.Logf("op: Put k-at    len=%d (== threshold %d) -> expect inline", len(at), threshold)
	if err := s.Put("k-at", at); err != nil {
		t.Fatalf("Put k-at: %v", err)
	}
	t.Logf("op: Put k-over  len=%d (>  threshold %d) -> expect overflow", len(over), threshold)
	if err := s.Put("k-over", over); err != nil {
		t.Fatalf("Put k-over: %v", err)
	}
	logState(t, s, "after boundary puts")

	if _, ok := s.BlockOf("k-at"); ok {
		t.Errorf("k-at: len == threshold must stay inline, got overflow")
	}
	if _, ok := s.BlockOf("k-over"); !ok {
		t.Errorf("k-over: len == threshold+1 must overflow, got inline")
	}
	if got, _ := s.Get("k-at"); !bytes.Equal(got, at) {
		t.Errorf("k-at value mismatch")
	}
	if got, _ := s.Get("k-over"); !bytes.Equal(got, over) {
		t.Errorf("k-over value mismatch")
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestUpdateRecyclesOldBlock(t *testing.T) {
	s, dir := openStore(t, 4, 16)

	if err := s.Put("k", mkval('x', 10)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	first, ok := s.BlockOf("k")
	if !ok {
		t.Fatalf("k must be overflow")
	}
	logState(t, s, "after first big put")

	t.Logf("op: Put k (new big value) -> write new block, update ref, then recycle block %d", first)
	if err := s.Put("k", mkval('y', 12)); err != nil {
		t.Fatalf("Put update: %v", err)
	}
	second, ok := s.BlockOf("k")
	if !ok {
		t.Fatalf("k must still be overflow")
	}
	logState(t, s, "after big update")

	if second <= first {
		t.Errorf("block numbers must be monotonically increasing: first=%d second=%d", first, second)
	}
	if _, err := os.Stat(filepath.Join(dir, blocksDir, fmt.Sprintf("%d%s", first, blockSuffix))); !os.IsNotExist(err) {
		t.Errorf("old block %d file must be recycled, stat err=%v", first, err)
	}
	if got := s.OverflowBlocks(); !reflect.DeepEqual(got, []uint64{second}) {
		t.Errorf("overflow blocks = %v, want [%d]", got, second)
	}
	if got, _ := s.Get("k"); !bytes.Equal(got, mkval('y', 12)) {
		t.Errorf("k value mismatch after update")
	}

	// 大值更新为小值：回收溢出块，变为内联。
	t.Logf("op: Put k (small value) -> update ref to inline, then recycle block %d", second)
	if err := s.Put("k", mkval('z', 3)); err != nil {
		t.Fatalf("Put shrink: %v", err)
	}
	logState(t, s, "after shrink to inline")
	if _, ok := s.BlockOf("k"); ok {
		t.Errorf("k must be inline after shrinking below threshold")
	}
	if got := s.OverflowBlocks(); len(got) != 0 {
		t.Errorf("overflow blocks = %v, want empty", got)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestDeleteRecyclesBlock(t *testing.T) {
	s, dir := openStore(t, 4, 16)

	if err := s.Put("victim", mkval('v', 9)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	blk, _ := s.BlockOf("victim")
	logState(t, s, "before delete")

	t.Logf("op: Delete victim -> remove record, recycle block %d", blk)
	if err := s.Delete("victim"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	logState(t, s, "after delete")

	if _, ok := s.Get("victim"); ok {
		t.Errorf("victim must be gone")
	}
	if got := s.OverflowBlocks(); len(got) != 0 {
		t.Errorf("overflow blocks = %v, want empty", got)
	}
	if _, err := os.Stat(filepath.Join(dir, blocksDir, fmt.Sprintf("%d%s", blk, blockSuffix))); !os.IsNotExist(err) {
		t.Errorf("block %d file must be recycled", blk)
	}
	if err := s.Delete("victim"); err != nil {
		t.Errorf("delete of missing key must be a no-op, got %v", err)
	}
}

func TestRecoveryReclaimsOrphans(t *testing.T) {
	s, dir := openStore(t, 4, 16)

	if err := s.Put("live", mkval('L', 10)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	liveBlk, _ := s.BlockOf("live")

	// 模拟崩溃：块已写入溢出表，但引用尚未持久化（成为孤儿块）。
	orphan := uint64(900)
	t.Logf("simulate crash: orphan block %d exists without any reference", orphan)
	if err := os.WriteFile(filepath.Join(dir, blocksDir, fmt.Sprintf("%d%s", orphan, blockSuffix)), mkval('o', 6), 0o644); err != nil {
		t.Fatalf("write orphan: %v", err)
	}

	s2 := reopen(t, dir, 4, 16)
	logState(t, s2, "after reopen")

	if got := s2.RecoveredOrphans(); !reflect.DeepEqual(got, []uint64{orphan}) {
		t.Errorf("recovered orphans = %v, want [%d]", got, orphan)
	}
	if _, err := os.Stat(filepath.Join(dir, blocksDir, fmt.Sprintf("%d%s", orphan, blockSuffix))); !os.IsNotExist(err) {
		t.Errorf("orphan block %d must be reclaimed", orphan)
	}
	if got := s2.OverflowBlocks(); !reflect.DeepEqual(got, []uint64{liveBlk}) {
		t.Errorf("overflow blocks = %v, want [%d]", got, liveBlk)
	}
	if got, _ := s2.Get("live"); !bytes.Equal(got, mkval('L', 10)) {
		t.Errorf("live value mismatch after recovery")
	}

	// 崩溃可能丢失计数器持久化：恢复后块号必须继续单调递增、不复用孤儿号。
	if err := s2.Put("next", mkval('N', 10)); err != nil {
		t.Fatalf("Put next: %v", err)
	}
	nextBlk, _ := s2.BlockOf("next")
	t.Logf("op: Put next -> allocated block %d (must be > orphan %d, never reused)", nextBlk, orphan)
	if nextBlk <= orphan {
		t.Errorf("block number reused: got %d, orphan was %d", nextBlk, orphan)
	}
	if err := s2.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestRecoveryDetectsDanglingRef(t *testing.T) {
	s, dir := openStore(t, 4, 16)

	if err := s.Put("ghost", mkval('g', 10)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	blk, _ := s.BlockOf("ghost")
	logState(t, s, "before simulated corruption")

	// 模拟块文件丢失：主记录仍引用它。
	t.Logf("simulate corruption: remove block %d while record still references it", blk)
	if err := os.Remove(filepath.Join(dir, blocksDir, fmt.Sprintf("%d%s", blk, blockSuffix))); err != nil {
		t.Fatalf("remove block: %v", err)
	}

	_, err := Open(Config{Dir: dir, Threshold: 4, MaxBlocks: 16})
	if !errors.Is(err, ErrDanglingRef) {
		t.Fatalf("reopen err = %v, want ErrDanglingRef", err)
	}
	t.Logf("reopen rejected with: %v", err)
}

func TestRejections(t *testing.T) {
	if _, err := Open(Config{Dir: t.TempDir(), Threshold: 0, MaxBlocks: 4}); !errors.Is(err, ErrInvalidThreshold) {
		t.Errorf("threshold=0: err = %v, want ErrInvalidThreshold", err)
	}
	if _, err := Open(Config{Dir: t.TempDir(), Threshold: -1, MaxBlocks: 4}); !errors.Is(err, ErrInvalidThreshold) {
		t.Errorf("threshold=-1: err = %v, want ErrInvalidThreshold", err)
	}
	if _, err := Open(Config{Dir: t.TempDir(), Threshold: 4, MaxBlocks: 0}); !errors.Is(err, ErrInvalidMaxBlocks) {
		t.Errorf("maxBlocks=0: err = %v, want ErrInvalidMaxBlocks", err)
	}

	s, _ := openStore(t, 4, 1)
	if err := s.Put("", mkval('a', 2)); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("empty key: err = %v, want ErrEmptyKey", err)
	}
	if err := s.Delete(""); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("delete empty key: err = %v, want ErrEmptyKey", err)
	}

	// 溢出块数上限为 1：第二个大值必须整体拒绝。
	if err := s.Put("big1", mkval('1', 10)); err != nil {
		t.Fatalf("Put big1: %v", err)
	}
	blocksBefore := s.OverflowBlocks()
	keysBefore := s.Keys()
	t.Logf("op: Put big2 with maxBlocks=1 already full -> must be rejected as a whole")
	if err := s.Put("big2", mkval('2', 10)); !errors.Is(err, ErrBlockLimit) {
		t.Fatalf("Put big2: err = %v, want ErrBlockLimit", err)
	}
	logState(t, s, "after rejected put")

	// 失败不得改变主记录、溢出表与块号计数。
	if got := s.OverflowBlocks(); !reflect.DeepEqual(got, blocksBefore) {
		t.Errorf("blocks changed after failed put: %v -> %v", blocksBefore, got)
	}
	if got := s.Keys(); !reflect.DeepEqual(got, keysBefore) {
		t.Errorf("keys changed after failed put: %v -> %v", keysBefore, got)
	}
	// 计数器未前进：替换 big1 应得到紧邻的下一个块号。
	blk1, _ := s.BlockOf("big1")
	if err := s.Put("big1", mkval('3', 10)); err != nil {
		t.Fatalf("replace big1: %v", err)
	}
	blk2, _ := s.BlockOf("big1")
	t.Logf("op: replace big1 -> block %d recycled, new block %d (== old+1 proves counter untouched by failure)", blk1, blk2)
	if blk2 != blk1+1 {
		t.Errorf("block counter advanced by failed put: got next=%d, want %d", blk2, blk1+1)
	}
	if got := s.OverflowBlocks(); !reflect.DeepEqual(got, []uint64{blk2}) {
		t.Errorf("overflow blocks = %v, want [%d]", got, blk2)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestConcurrentReadsAndVerify(t *testing.T) {
	s, _ := openStore(t, 8, 64)

	want := map[string][]byte{}
	for i := 0; i < 8; i++ {
		k := fmt.Sprintf("key-%d", i)
		v := mkval(byte('A'+i), 4+i*4) // 混合内联与溢出
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put %s: %v", k, err)
		}
		want[k] = v
	}
	logState(t, s, "fixture")

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for iter := 0; iter < 200; iter++ {
				for k, v := range want {
					got, ok := s.Get(k)
					if !ok {
						errs <- fmt.Errorf("reader %d: key %s missing", g, k)
						return
					}
					if !bytes.Equal(got, v) {
						errs <- fmt.Errorf("reader %d: key %s value mismatch", g, k)
						return
					}
				}
				if err := s.Verify(); err != nil {
					errs <- fmt.Errorf("reader %d: verify: %w", g, err)
					return
				}
				_ = s.OverflowBlocks()
				_ = s.Keys()
			}
		}(g)
	}
	// 并发写入，验证读写与自检可并发且任意时刻不变量成立。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			k := fmt.Sprintf("churn-%d", i%4)
			if err := s.Put(k, mkval(byte('a'+i%26), 20+i%30)); err != nil {
				errs <- fmt.Errorf("churn put: %w", err)
				return
			}
			if err := s.Delete(k); err != nil {
				errs <- fmt.Errorf("churn delete: %w", err)
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	t.Logf("concurrent phase done: 8 readers x 200 iters + churn writer, no mismatch")
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestNaiveReplay 朴素重放核对：把同一串确定性随机操作同时施加到
// Store 和一个朴素 map 模型上，逐步比对所有键的取值，并校验溢出不变量。
func TestNaiveReplay(t *testing.T) {
	const threshold = 16
	s, dir := openStore(t, threshold, 128)
	model := map[string][]byte{}
	rng := rand.New(rand.NewSource(42))

	apply := func(op int) {
		k := fmt.Sprintf("k%d", rng.Intn(12))
		switch op {
		case 0, 1, 2: // put，长度跨阈值两侧
			n := rng.Intn(3 * threshold)
			v := mkval(byte(rng.Intn(26)+'a'), n)
			if err := s.Put(k, v); err != nil {
				t.Fatalf("Put %s: %v", k, err)
			}
			model[k] = v
			t.Logf("op: Put %s len=%d (%s)", k, n, map[bool]string{true: "overflow", false: "inline"}[n > threshold])
		default: // delete
			if err := s.Delete(k); err != nil {
				t.Fatalf("Delete %s: %v", k, err)
			}
			delete(model, k)
			t.Logf("op: Delete %s", k)
		}
	}

	check := func(step string) {
		t.Helper()
		for _, k := range s.Keys() {
			got, _ := s.Get(k)
			if !bytes.Equal(got, model[k]) {
				t.Fatalf("%s: key %s = %v, model has %v", step, k, got, model[k])
			}
		}
		if len(s.Keys()) != len(model) {
			t.Fatalf("%s: store has %d keys, model has %d", step, len(s.Keys()), len(model))
		}
		if err := s.Verify(); err != nil {
			t.Fatalf("%s: Verify: %v", step, err)
		}
	}

	for i := 0; i < 120; i++ {
		apply(rng.Intn(4))
		if i%20 == 0 {
			check(fmt.Sprintf("step %d", i))
		}
	}
	check("final")
	logState(t, s, "final in-memory state")

	// 重启后再核对一次：持久化 + 恢复的结果必须与朴素模型一致。
	s2 := reopen(t, dir, threshold, 128)
	logState(t, s2, "after reopen")
	for k, v := range model {
		got, ok := s2.Get(k)
		if !ok || !bytes.Equal(got, v) {
			t.Fatalf("after reopen: key %s mismatch", k)
		}
	}
	if len(s2.Keys()) != len(model) {
		t.Fatalf("after reopen: store has %d keys, model has %d", len(s2.Keys()), len(model))
	}
	t.Logf("naive replay check passed: %d keys identical after reopen", len(model))
}
