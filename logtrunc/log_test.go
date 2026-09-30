package logtrunc

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func openTestLog(t *testing.T, dir string, opts *Options) *Log {
	t.Helper()
	if opts == nil {
		opts = &Options{}
	}
	if opts.Logger == nil {
		opts.Logger = func(format string, args ...any) { t.Logf(format, args...) }
	}
	l, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return l
}

func appendN(t *testing.T, l *Log, n int) {
	t.Helper()
	payloads := make([][]byte, n)
	for i := range payloads {
		payloads[i] = []byte(fmt.Sprintf("payload-%d", l.Next()+uint64(i)))
	}
	if _, err := l.Append(payloads...); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

func mustDeclare(t *testing.T, l *Log, upTo uint64) {
	t.Helper()
	if err := l.DeclarePersisted(upTo); err != nil {
		t.Fatalf("DeclarePersisted(%d): %v", upTo, err)
	}
}

// checkState 校验一次失败操作没有改变日志、持久化位点与标记。
func checkState(t *testing.T, l *Log, start, persisted, marker, next uint64) {
	t.Helper()
	if got := l.Start(); got != start {
		t.Fatalf("start = %d, want %d（失败操作不得改变日志）", got, start)
	}
	if got := l.Persisted(); got != persisted {
		t.Fatalf("persisted = %d, want %d（失败操作不得改变持久化位点）", got, persisted)
	}
	if got := l.Marker(); got != marker {
		t.Fatalf("marker = %d, want %d（失败操作不得改变标记）", got, marker)
	}
	if got := l.Next(); got != next {
		t.Fatalf("next = %d, want %d", got, next)
	}
	if err := l.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestTruncateRequiresPersisted 截断位点之前必已持久化，否则整体拒绝。
func TestTruncateRequiresPersisted(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, nil)

	appendN(t, l, 10)
	mustDeclare(t, l, 5)

	t.Logf("操作: truncate(7) persisted=%d marker=%d actual=%d", l.Persisted(), l.Marker(), l.Start())
	err := l.Truncate(7)
	if !errors.Is(err, ErrTruncateBeyondPersisted) {
		t.Fatalf("err = %v, want ErrTruncateBeyondPersisted", err)
	}
	t.Logf("判定依据: to=7 > persisted=5，前缀未全部落盘，整体拒绝")
	checkState(t, l, 0, 5, 0, 10)

	mustDeclare(t, l, 10)
	if err := l.Truncate(6); err != nil {
		t.Fatalf("Truncate(6): %v", err)
	}
	t.Logf("判定依据: to=6 <= persisted=10，允许截断")
	checkState(t, l, 6, 10, 6, 10)

	entries, err := l.Read(0, 10)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 4 || entries[0].Offset != 6 || entries[3].Offset != 9 {
		t.Fatalf("entries = %+v, want offsets 6..9", entries)
	}
}

// TestDeclarePersistedValidation 持久化声明回退与越界都整体拒绝。
func TestDeclarePersistedValidation(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, nil)

	appendN(t, l, 8)
	mustDeclare(t, l, 5)

	t.Logf("操作: declare(3) persisted=%d marker=%d actual=%d", l.Persisted(), l.Marker(), l.Start())
	if err := l.DeclarePersisted(3); !errors.Is(err, ErrPersistRegression) {
		t.Fatalf("err = %v, want ErrPersistRegression", err)
	}
	t.Logf("判定依据: upTo=3 < persisted=5，位点只进不退，整体拒绝")
	checkState(t, l, 0, 5, 0, 8)

	t.Logf("操作: declare(9) persisted=%d marker=%d actual=%d", l.Persisted(), l.Marker(), l.Start())
	if err := l.DeclarePersisted(9); !errors.Is(err, ErrPersistOutOfRange) {
		t.Fatalf("err = %v, want ErrPersistOutOfRange", err)
	}
	t.Logf("判定依据: upTo=9 > next=8，声明越界，整体拒绝")
	checkState(t, l, 0, 5, 0, 8)

	mustDeclare(t, l, 8)
	checkState(t, l, 0, 8, 0, 8)
}

// TestTruncateOutOfRange 截断越界（小于实际起始或大于下一偏移）整体拒绝。
func TestTruncateOutOfRange(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, nil)

	appendN(t, l, 10)
	mustDeclare(t, l, 10)
	if err := l.Truncate(4); err != nil {
		t.Fatalf("Truncate(4): %v", err)
	}

	t.Logf("操作: truncate(2) persisted=%d marker=%d actual=%d", l.Persisted(), l.Marker(), l.Start())
	if err := l.Truncate(2); !errors.Is(err, ErrTruncateOutOfRange) {
		t.Fatalf("err = %v, want ErrTruncateOutOfRange", err)
	}
	t.Logf("判定依据: to=2 < actual=4，截断位点越界，整体拒绝")
	checkState(t, l, 4, 10, 4, 10)

	t.Logf("操作: truncate(11) persisted=%d marker=%d actual=%d", l.Persisted(), l.Marker(), l.Start())
	if err := l.Truncate(11); !errors.Is(err, ErrTruncateOutOfRange) {
		t.Fatalf("err = %v, want ErrTruncateOutOfRange", err)
	}
	t.Logf("判定依据: to=11 > next=10，截断位点越界，整体拒绝")
	checkState(t, l, 4, 10, 4, 10)
}

var errSimulatedCrash = errors.New("simulated crash between marker and delete")

// TestCrashBetweenMarkerAndDelete 截断两步之间崩溃，恢复时补删收敛。
func TestCrashBetweenMarkerAndDelete(t *testing.T) {
	dir := t.TempDir()
	crashed := false
	l := openTestLog(t, dir, &Options{CrashHook: func() {
		t.Logf("注入崩溃: 标记已落盘，物理删除未执行")
		panic(errSimulatedCrash)
	}})

	appendN(t, l, 10)
	mustDeclare(t, l, 10)

	func() {
		defer func() {
			if r := recover(); r != errSimulatedCrash {
				t.Fatalf("recover = %v, want simulated crash", r)
			}
			crashed = true
		}()
		_ = l.Truncate(6)
	}()
	if !crashed {
		t.Fatal("expected simulated crash")
	}
	t.Logf("崩溃现场: marker 文件=6, 数据文件 actual=0（未删除）")

	// 模拟进程重启：丢弃旧实例，重新 Open。
	l2 := openTestLog(t, dir, nil)
	t.Logf("恢复后: persisted=%d marker=%d actual=%d", l2.Persisted(), l2.Marker(), l2.Start())
	checkState(t, l2, 6, 10, 6, 10)

	entries, err := l2.Read(0, 10)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 4 || entries[0].Offset != 6 {
		t.Fatalf("entries = %+v, want offsets 6..9", entries)
	}
}

// TestCleanReopen 无崩溃重开：标记与实际起始一致，判定干净。
func TestCleanReopen(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, nil)
	appendN(t, l, 10)
	mustDeclare(t, l, 10)
	if err := l.Truncate(6); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	l2 := openTestLog(t, dir, nil)
	t.Logf("恢复后: persisted=%d marker=%d actual=%d 判定=干净", l2.Persisted(), l2.Marker(), l2.Start())
	checkState(t, l2, 6, 10, 6, 10)
}

// TestOverDeletionDetected 实际起始大于标记说明发生越删，恢复整体拒绝。
func TestOverDeletionDetected(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, nil)
	appendN(t, l, 10)
	mustDeclare(t, l, 10)
	if err := l.Truncate(8); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	// 人为把标记回拨到 5，模拟“越删”：actual=8 > marker=5。
	if err := os.WriteFile(filepath.Join(dir, markerFileName), []byte("5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("构造现场: actual=8 marker=5")

	_, err := Open(dir, &Options{Logger: func(format string, args ...any) { t.Logf(format, args...) }})
	if !errors.Is(err, ErrOverDeletion) {
		t.Fatalf("err = %v, want ErrOverDeletion", err)
	}
	t.Logf("判定依据: actual=8 > marker=5，发生越删，报告损坏并拒绝打开")
}

// TestConcurrentReadVerify 读与自检可并发调用，且与追加、截断并发；
// 任一读到的区间必须连贯，不得出现标记已推进但前缀部分可见的中间态。
func TestConcurrentReadVerify(t *testing.T) {
	dir := t.TempDir()
	// 并发用例操作量大，日志采样打印避免刷屏；判定类用例仍全量打印。
	var logCount atomic.Int64
	l := openTestLog(t, dir, &Options{Logger: func(format string, args ...any) {
		if logCount.Add(1)%500 == 1 {
			t.Logf(format, args...)
		}
	}})

	const writers = 2
	const readers = 4
	const rounds = 200

	var wg sync.WaitGroup
	var recyclerWg sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				payloads := [][]byte{[]byte(fmt.Sprintf("w%d-%d", id, i))}
				if _, err := l.Append(payloads...); err != nil {
					t.Errorf("Append: %v", err)
					return
				}
				l.DeclarePersisted(l.Next()) // 追加即声明，简化并发模型
			}
		}(w)
	}

	// 周期回收协程。
	recyclerWg.Add(1)
	go func() {
		defer recyclerWg.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
				l.RecycleOnce(10)
			}
		}
	}()

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				if err := l.Verify(); err != nil {
					t.Errorf("Verify: %v", err)
					return
				}
				start, next := l.Start(), l.Next()
				if start == next {
					continue
				}
				entries, err := l.Read(start, next)
				if errors.Is(err, ErrReadOutOfRange) {
					continue // 并发截断使快照过期，属正常结果，重试即可
				}
				if err != nil {
					t.Errorf("Read: %v", err)
					return
				}
				if len(entries) == 0 {
					t.Errorf("读到空区间但 start=%d < next=%d", start, next)
					return
				}
				for j, e := range entries {
					if want := entries[0].Offset + uint64(j); e.Offset != want {
						t.Errorf("读到不连贯区间: got %d want %d（疑似截断中间态）", e.Offset, want)
						return
					}
				}
				if entries[0].Offset < start {
					t.Errorf("读到已截断前缀: first=%d < start=%d（标记已推进但前缀可见）", entries[0].Offset, start)
					return
				}
			}
		}()
	}

	wg.Wait()
	close(stop)
	recyclerWg.Wait()
	t.Logf("并发结束: persisted=%d marker=%d actual=%d next=%d", l.Persisted(), l.Marker(), l.Start(), l.Next())
	if err := l.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestNaiveCrossCheck 朴素模型核对：朴素追加再删除已截断前缀，
// 与实现逐次比对读出的内容。
func TestNaiveCrossCheck(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, nil)

	rng := rand.New(rand.NewSource(42))
	var naive [][]byte // 朴素模型：只追加，按截断位点删前缀
	naiveStart := uint64(0)

	for round := 0; round < 100; round++ {
		// 随机追加 1~5 条。
		n := 1 + rng.Intn(5)
		payloads := make([][]byte, n)
		for i := range payloads {
			payloads[i] = []byte(fmt.Sprintf("r%d-%d", round, i))
		}
		if _, err := l.Append(payloads...); err != nil {
			t.Fatalf("Append: %v", err)
		}
		naive = append(naive, payloads...)

		// 声明持久化到当前末尾。
		mustDeclare(t, l, l.Next())

		// 随机截断到 [naiveStart, persisted] 内某点。
		to := naiveStart + uint64(rng.Intn(int(l.Persisted()-naiveStart)+1))
		if err := l.Truncate(to); err != nil {
			t.Fatalf("Truncate(%d): %v", to, err)
		}
		naive = naive[to-naiveStart:]
		naiveStart = to

		// 核对：实现读出的内容与朴素模型完全一致。
		got, err := l.Read(0, l.Next())
		if err != nil && !errors.Is(err, ErrReadOutOfRange) {
			t.Fatalf("Read: %v", err)
		}
		if len(got) != len(naive) {
			t.Fatalf("round %d: len = %d, want %d", round, len(got), len(naive))
		}
		for i, e := range got {
			if e.Offset != naiveStart+uint64(i) || string(e.Payload) != string(naive[i]) {
				t.Fatalf("round %d: entry %d = {%d %s}, want {%d %s}",
					round, i, e.Offset, e.Payload, naiveStart+uint64(i), naive[i])
			}
		}
	}
	t.Logf("核对通过: rounds=100 persisted=%d marker=%d actual=%d 剩余=%d 条",
		l.Persisted(), l.Marker(), l.Start(), len(naive))

	// 崩溃重启后再核对一次，保证可复现。
	l2 := openTestLog(t, dir, nil)
	got, err := l2.Read(0, l2.Next())
	if err != nil && !errors.Is(err, ErrReadOutOfRange) {
		t.Fatalf("Read after reopen: %v", err)
	}
	if len(got) != len(naive) {
		t.Fatalf("after reopen: len = %d, want %d", len(got), len(naive))
	}
	for i, e := range got {
		if string(e.Payload) != string(naive[i]) {
			t.Fatalf("after reopen: entry %d = %s, want %s", i, e.Payload, naive[i])
		}
	}
	t.Logf("重启核对通过: persisted=%d marker=%d actual=%d", l2.Persisted(), l2.Marker(), l2.Start())
}
