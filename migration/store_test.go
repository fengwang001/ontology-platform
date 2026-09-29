package migration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// memLogger 收集日志，便于断言每步输入、返回与判定依据均被打印。
type memLogger struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (l *memLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.buf, format+"\n", args...)
}

func (l *memLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func (l *memLogger) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.Reset()
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

// detStep 是确定性迁移：输出为字节 b 重复（长度 = 输入长度+1）。
func detStep(b byte) Func {
	return func(_ context.Context, _ Version, data []byte) ([]byte, error) {
		return bytes.Repeat([]byte{b}, len(data)+1), nil
	}
}

func newTestStore(t *testing.T, initial Version) (*Store, *memLogger) {
	t.Helper()
	l := &memLogger{}
	s, err := NewStore(initial, WithLogger(l))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s, l
}

func mustRegister(t *testing.T, s *Store, from Version, fn Func) {
	t.Helper()
	if err := s.Register(from, fn); err != nil {
		t.Fatalf("Register(v%d): %v", from, err)
	}
}

func mustUpgrade(t *testing.T, s *Store, next Version) {
	t.Helper()
	if err := s.Upgrade(next); err != nil {
		t.Fatalf("Upgrade(%d): %v", next, err)
	}
}

// naiveReference 是朴素参照：从原始 v1 数据重跑整条确定性链。
func naiveReference(data []byte, current Version) []byte {
	out := data
	for v := Version(1); v < current; v++ {
		out = bytes.Repeat([]byte{byte('0' + v + 1)}, len(out)+1)
	}
	return out
}

func TestMultiVersionChainMatchesNaiveReference(t *testing.T) {
	ctx := context.Background()
	s, logBuf := newTestStore(t, 1)
	original := []byte("A")
	if err := s.Write(ctx, "k", original); err != nil {
		t.Fatalf("Write: %v", err)
	}

	mustUpgrade(t, s, 2)
	mustUpgrade(t, s, 3)
	mustUpgrade(t, s, 4)
	mustRegister(t, s, 1, detStep('2'))
	mustRegister(t, s, 2, detStep('3'))
	mustRegister(t, s, 3, detStep('4'))

	if sv, _ := s.StoredVersion("k"); sv != 1 {
		t.Fatalf("before read StoredVersion = %d, want 1", sv)
	}

	got, err := s.Read(ctx, "k")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if want := naiveReference(original, 4); !bytes.Equal(got, want) {
		t.Fatalf("migrated = %q, want naive reference %q", got, want)
	}
	if sv, _ := s.StoredVersion("k"); sv != 4 {
		t.Fatalf("after read StoredVersion = %d, want 4", sv)
	}

	// 可复现：再次读取逐字节一致且走快速路径，已执行步骤不重复。
	logBuf.reset()
	got2, err := s.Read(ctx, "k")
	if err != nil || !bytes.Equal(got2, got) {
		t.Fatalf("second read = %q, %v; want %q", got2, err, got)
	}
	logs := logBuf.String()
	if !contains(logs, "decision=fast_path") {
		t.Fatalf("second read did not take fast path; log:\n%s", logs)
	}
	if contains(logs, "run_chain") || contains(logs, "step key") {
		t.Fatalf("steps re-executed after writeback; log:\n%s", logs)
	}

	// 首次迁移日志须含每步输入、返回与判定依据：用独立存储重放一次。
	s3, logBuf3 := newTestStore(t, 1)
	mustRegister(t, s3, 1, detStep('2'))
	mustRegister(t, s3, 2, detStep('3'))
	mustRegister(t, s3, 3, detStep('4'))
	if err := s3.Write(ctx, "k", original); err != nil {
		t.Fatal(err)
	}
	mustUpgrade(t, s3, 2)
	mustUpgrade(t, s3, 3)
	mustUpgrade(t, s3, 4)
	if _, err := s3.Read(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	logs = logBuf3.String()
	for _, want := range []string{
		"v1->v2: input_bytes=1",
		"v1->v2: return_bytes=2: decision=ok",
		"v2->v3: input_bytes=2",
		"v3->v4",
		"decision=run_chain",
		"decision=committed",
	} {
		if !contains(logs, want) {
			t.Fatalf("log missing %q; log:\n%s", want, logs)
		}
	}
}

func TestOnlyStepsAfterStoredVersionRun(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, 1)
	var c1, c2, c3 atomic.Int32
	counting := func(c *atomic.Int32, b byte) Func {
		return func(_ context.Context, _ Version, data []byte) ([]byte, error) {
			c.Add(1)
			return bytes.Repeat([]byte{b}, len(data)+1), nil
		}
	}
	mustRegister(t, s, 1, counting(&c1, '2'))
	mustRegister(t, s, 2, counting(&c2, '3'))
	mustRegister(t, s, 3, counting(&c3, '4'))
	if err := s.Write(ctx, "k", []byte("AA")); err != nil {
		t.Fatal(err)
	}

	mustUpgrade(t, s, 2)
	mustUpgrade(t, s, 3)
	if _, err := s.Read(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if c1.Load() != 1 || c2.Load() != 1 || c3.Load() != 0 {
		t.Fatalf("counts after v3 read = %d,%d,%d; want 1,1,0", c1.Load(), c2.Load(), c3.Load())
	}

	mustUpgrade(t, s, 4)
	if _, err := s.Read(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	// 只迁移存储版本（v3）之后的步骤：前两步不重复。
	if c1.Load() != 1 || c2.Load() != 1 || c3.Load() != 1 {
		t.Fatalf("counts after v4 read = %d,%d,%d; want 1,1,1", c1.Load(), c2.Load(), c3.Load())
	}
}

func TestMissingMigrationInvokesNothing(t *testing.T) {
	ctx := context.Background()
	s, logBuf := newTestStore(t, 1)
	if err := s.Write(ctx, "k", []byte("v1data")); err != nil {
		t.Fatal(err)
	}
	mustUpgrade(t, s, 2)
	mustUpgrade(t, s, 3)

	var calls atomic.Int32
	mk := func() Func {
		return func(context.Context, Version, []byte) ([]byte, error) {
			calls.Add(1)
			return []byte("x"), nil
		}
	}
	// 链 v1->v2->v3 只登记 v2->v3，缺 v1->v2。
	mustRegister(t, s, 2, mk())

	_, err := s.Read(ctx, "k")
	if !errors.Is(err, ErrMissingMigration) {
		t.Fatalf("Read err = %v, want ErrMissingMigration", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("migration functions called %d times despite missing step", calls.Load())
	}
	if sv, _ := s.StoredVersion("k"); sv != 1 {
		t.Fatalf("storage version changed to %d after missing-chain rejection", sv)
	}
	if err := s.SelfCheck(ctx); !errors.Is(err, ErrMissingMigration) {
		t.Fatalf("SelfCheck err = %v, want ErrMissingMigration", err)
	}
	if !contains(logBuf.String(), "calls=0") {
		t.Fatalf("missing-chain log missing calls=0 marker; log:\n%s", logBuf.String())
	}

	// 补齐链路后读取应成功。
	mustRegister(t, s, 1, mk())
	if _, err := s.Read(ctx, "k"); err != nil {
		t.Fatalf("Read after completing chain: %v", err)
	}
	if err := s.SelfCheck(ctx); err != nil {
		t.Fatalf("SelfCheck after completing chain: %v", err)
	}
}

func TestMigrationFailureKeepsStorageUntouchedAndRetryable(t *testing.T) {
	ctx := context.Background()
	s, logBuf := newTestStore(t, 1)
	original := []byte("v1data")
	if err := s.Write(ctx, "k", original); err != nil {
		t.Fatal(err)
	}
	mustUpgrade(t, s, 2)
	mustUpgrade(t, s, 3)

	var v1Calls, v2Calls, v2Failures atomic.Int32
	stepErr := errors.New("boom at v2")
	mustRegister(t, s, 1, func(context.Context, Version, []byte) ([]byte, error) {
		v1Calls.Add(1)
		return []byte("v2"), nil
	})
	mustRegister(t, s, 2, func(context.Context, Version, []byte) ([]byte, error) {
		v2Calls.Add(1)
		if v2Failures.Add(1) == 1 {
			return nil, stepErr // 第一次失败，之后成功
		}
		return []byte("v3"), nil
	})

	_, err := s.Read(ctx, "k")
	if !errors.Is(err, ErrMigrationFailed) || !errors.Is(err, stepErr) {
		t.Fatalf("Read err = %v, want ErrMigrationFailed wrapping %v", err, stepErr)
	}
	if sv, _ := s.StoredVersion("k"); sv != 1 {
		t.Fatalf("stored version = %d, want 1 after failure", sv)
	}
	s.mu.RLock()
	stored := string(s.data["k"].data)
	s.mu.RUnlock()
	if stored != string(original) {
		t.Fatalf("stored data = %q, want %q after failure", stored, original)
	}
	if !contains(logBuf.String(), "decision=keep_storage_unchanged") {
		t.Fatalf("failure log missing marker; log:\n%s", logBuf.String())
	}

	// 再次读取：整链从头重跑并成功，结果与从原始 v1 成功跑通一致。
	got, err := s.Read(ctx, "k")
	if err != nil {
		t.Fatalf("retry Read: %v", err)
	}
	if string(got) != "v3" {
		t.Fatalf("retry result = %q, want v3", got)
	}
	if v1Calls.Load() != 2 || v2Calls.Load() != 2 {
		t.Fatalf("retry did not rerun whole chain: v1=%d v2=%d", v1Calls.Load(), v2Calls.Load())
	}
	if sv, _ := s.StoredVersion("k"); sv != 3 {
		t.Fatalf("stored version after retry = %d, want 3", sv)
	}

	// 写回成功后步骤不再重复。
	if _, err := s.Read(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if v1Calls.Load() != 2 || v2Calls.Load() != 2 {
		t.Fatalf("steps re-executed after successful writeback: v1=%d v2=%d", v1Calls.Load(), v2Calls.Load())
	}
}

func TestPanicTreatedAsFailureAndLeavesNoFlight(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, 1)
	if err := s.Write(ctx, "k", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	mustUpgrade(t, s, 2)
	mustRegister(t, s, 1, func(context.Context, Version, []byte) ([]byte, error) {
		panic("kaboom")
	})
	_, err := s.Read(ctx, "k")
	if !errors.Is(err, ErrMigrationFailed) {
		t.Fatalf("Read err = %v, want ErrMigrationFailed after panic", err)
	}
	if sv, _ := s.StoredVersion("k"); sv != 1 {
		t.Fatalf("storage changed after panic: version=%d", sv)
	}
	// flight 已清理：后续写入立即完成，不挂起。
	done := make(chan error, 1)
	go func() { done <- s.Write(ctx, "k", []byte("fresh")) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Write after failed migration: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked forever by leftover flight")
	}
}

func TestInvalidInputsRejectedAndDistinguishable(t *testing.T) {
	ctx := context.Background()

	if _, err := NewStore(0); !errors.Is(err, ErrInvalidVersion) {
		t.Fatalf("NewStore(0) err = %v, want ErrInvalidVersion", err)
	}

	s, _ := newTestStore(t, 2)

	if err := s.Register(0, detStep('x')); !errors.Is(err, ErrInvalidVersion) {
		t.Fatalf("Register(0) err = %v, want ErrInvalidVersion", err)
	}
	if err := s.Register(1, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Register(nil) err = %v, want ErrInvalidArgument", err)
	}
	mustRegister(t, s, 1, detStep('2'))
	if err := s.Register(1, detStep('2')); !errors.Is(err, ErrMigrationExists) {
		t.Fatalf("duplicate Register err = %v, want ErrMigrationExists", err)
	}

	for _, next := range []Version{0, 1, 2} {
		if err := s.Upgrade(next); !errors.Is(err, ErrInvalidVersion) {
			t.Fatalf("Upgrade(%d) err = %v, want ErrInvalidVersion", next, err)
		}
	}

	if err := s.Write(ctx, "", []byte("x")); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("Write empty key err = %v, want ErrInvalidKey", err)
	}
	if _, err := s.Read(ctx, ""); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("Read empty key err = %v, want ErrInvalidKey", err)
	}
	if _, err := s.StoredVersion(""); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("StoredVersion empty key err = %v, want ErrInvalidKey", err)
	}
	if ok, err := s.Exists(""); err == nil || ok || !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("Exists empty key = %v,%v; want false,ErrInvalidKey", ok, err)
	}
	if _, err := s.Read(ctx, "missing"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Read missing err = %v, want ErrKeyNotFound", err)
	}
	if _, err := s.StoredVersion("missing"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("StoredVersion missing err = %v, want ErrKeyNotFound", err)
	}
	if ok, _ := s.Exists("missing"); ok {
		t.Fatal("Exists(missing) = true, want false")
	}

	// 所有被拒操作之后，状态必须完全不变。
	if s.CurrentVersion() != 2 {
		t.Fatalf("current version = %d, want 2 after rejections", s.CurrentVersion())
	}
	s.mu.RLock()
	funcCount, keyCount := len(s.funcs), len(s.data)
	s.mu.RUnlock()
	if funcCount != 1 || keyCount != 0 {
		t.Fatalf("state mutated by rejected calls: funcs=%d keys=%d", funcCount, keyCount)
	}

	// 错误哨兵两两不同，可被 errors.Is 区分。
	sentinels := []error{
		ErrInvalidVersion, ErrInvalidKey, ErrInvalidArgument,
		ErrKeyNotFound, ErrMissingMigration, ErrMigrationFailed, ErrMigrationExists,
	}
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if errors.Is(sentinels[i], sentinels[j]) {
				t.Fatalf("sentinels %v and %v are not distinguishable", sentinels[i], sentinels[j])
			}
		}
	}
}

func TestWriteStoresCopyAndReadReturnsCopy(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, 1)
	in := []byte("hello")
	if err := s.Write(ctx, "k", in); err != nil {
		t.Fatal(err)
	}
	in[0] = 'X' // 改调用方缓冲区，不影响存储

	out, err := s.Read(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "hello" {
		t.Fatalf("storage aliased write buffer: %q", out)
	}
	out[0] = 'Y' // 改返回缓冲区，不影响存储
	out2, _ := s.Read(ctx, "k")
	if string(out2) != "hello" {
		t.Fatalf("storage aliased read buffer: %q", out2)
	}
}

func TestConcurrentSameKeyMigrationRunsOnce(t *testing.T) {
	ctx := context.Background()
	// 用独立存储统计并发下迁移函数的执行次数。
	s2, _ := newTestStore(t, 1)
	var total atomic.Int32
	slow := func(c *atomic.Int32) Func {
		return func(ctx context.Context, from Version, data []byte) ([]byte, error) {
			c.Add(1)
			time.Sleep(150 * time.Millisecond) // 留出窗口让并发读者 join_inflight
			return bytes.Repeat(data, 2), nil
		}
	}
	mustRegister(t, s2, 1, slow(&total))
	mustRegister(t, s2, 2, func(context.Context, Version, []byte) ([]byte, error) {
		return []byte("done"), nil
	})
	if err := s2.Write(ctx, "hot", []byte("z")); err != nil {
		t.Fatal(err)
	}
	mustUpgrade(t, s2, 2)
	mustUpgrade(t, s2, 3)

	const n = 16
	var wg sync.WaitGroup
	results := make([][]byte, n)
	errs := make([]error, n)
	start := make(chan struct{})
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = s2.Read(ctx, "hot")
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("reader %d: %v", i, err)
		}
	}
	if total.Load() != 1 {
		t.Fatalf("v1->v2 executed %d times for same key, want 1", total.Load())
	}
	for i := 1; i < n; i++ {
		if !bytes.Equal(results[i], results[0]) {
			t.Fatalf("reader %d got %q, reader 0 got %q", i, results[i], results[0])
		}
	}
	// 写回后再并发读取：快速路径，不执行任何步骤。
	var wg2 sync.WaitGroup
	wg2.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg2.Done()
			if _, err := s2.Read(ctx, "hot"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg2.Wait()
	if total.Load() != 1 {
		t.Fatalf("steps re-executed after writeback: total=%d", total.Load())
	}
}

func TestConcurrentMixedOperations(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, 1)
	mustRegister(t, s, 1, detStep('2'))
	mustRegister(t, s, 2, detStep('3'))

	const writers = 8
	const readers = 8
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 初始数据
	for k := 0; k < writers; k++ {
		if err := s.Write(ctx, fmt.Sprintf("k%d", k), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	mustUpgrade(t, s, 2)
	mustUpgrade(t, s, 3)

	for k := 0; k < readers; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", k%writers)
			for {
				select {
				case <-stop:
					return
				default:
				}
				out, err := s.Read(ctx, key)
				if err != nil {
					t.Errorf("read %s: %v", key, err)
					return
				}
				want := naiveReference([]byte("v"), 3)
				if !bytes.Equal(out, want) {
					// 该键可能被并发 Write 以 v3 覆写为别的内容；
					// 本用例写者只写初始一次，所以结果必须恒定。
					t.Errorf("read %s = %q, want %q", key, out, want)
					return
				}
			}
		}(k)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := s.StoredVersion("k0"); err != nil && !errors.Is(err, ErrKeyNotFound) {
				t.Errorf("StoredVersion: %v", err)
				return
			}
			if _, err := s.Exists("k1"); err != nil {
				t.Errorf("Exists: %v", err)
				return
			}
			if err := s.SelfCheck(ctx); err != nil {
				t.Errorf("SelfCheck: %v", err)
				return
			}
		}
	}()

	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
}
