package truncation

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// dump 打印操作、持久化位点、截断标记、实际起始与判定依据。
func dump(t *testing.T, l *Log, op, basis string) {
	t.Helper()
	st := l.Status()
	t.Logf("op=%-28s durable=%d marker=%d start=%d end=%d entries=%d | 判定依据: %s",
		op, st.Durable, st.Marker, st.Start, st.End, st.Entries, basis)
}

func openTemp(t *testing.T) *Log {
	t.Helper()
	l, err := Open(filepath.Join(t.TempDir(), "log"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return l
}

func appendN(t *testing.T, l *Log, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := l.Append([]byte(fmt.Sprintf("entry-%d", i))); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
}

// 截断前必已持久化：未持久化的前缀拒绝截断，且失败不改变任何状态。
func TestTruncateRequiresDurable(t *testing.T) {
	l := openTemp(t)
	appendN(t, l, 10)
	dump(t, l, "append x10", "初始：durable=0，任何截断都应拒绝")

	if err := l.DeclareDurable(5); err != nil {
		t.Fatalf("DeclareDurable(5): %v", err)
	}
	dump(t, l, "DeclareDurable(5)", "持久化位点推进到 5")

	before := l.Status()
	err := l.Truncate(7)
	if !errors.Is(err, ErrTruncateNotDurable) {
		t.Fatalf("Truncate(7) want ErrTruncateNotDurable, got %v", err)
	}
	dump(t, l, "Truncate(7) rejected", "target=7 > durable=5，前缀未全部持久化")
	if after := l.Status(); after != before {
		t.Fatalf("failed truncate changed state: before=%+v after=%+v", before, after)
	}

	if err := l.DeclareDurable(10); err != nil {
		t.Fatalf("DeclareDurable(10): %v", err)
	}
	if err := l.Truncate(5); err != nil {
		t.Fatalf("Truncate(5): %v", err)
	}
	dump(t, l, "Truncate(5)", "target=5 <= durable=10，截断成功：marker=5 start=6")
	if st := l.Status(); st.Start != 6 || st.Marker != 5 || st.Entries != 5 {
		t.Fatalf("unexpected status: %+v", st)
	}
}

// 持久化声明越界与回退：整体拒绝且状态不变。
func TestDurableDeclareValidation(t *testing.T) {
	l := openTemp(t)
	appendN(t, l, 5)

	before := l.Status()
	if err := l.DeclareDurable(6); !errors.Is(err, ErrDurableOutOfRange) {
		t.Fatalf("DeclareDurable(6) want ErrDurableOutOfRange, got %v", err)
	}
	dump(t, l, "DeclareDurable(6) rejected", "offset=6 > end=5，越界")

	if err := l.DeclareDurable(4); err != nil {
		t.Fatalf("DeclareDurable(4): %v", err)
	}
	if err := l.DeclareDurable(3); !errors.Is(err, ErrDurableRegression) {
		t.Fatalf("DeclareDurable(3) want ErrDurableRegression, got %v", err)
	}
	dump(t, l, "DeclareDurable(3) rejected", "offset=3 < durable=4，回退")

	st := l.Status()
	if st.Durable != 4 || st.Entries != before.Entries || st.Marker != before.Marker {
		t.Fatalf("failed declares changed state: %+v", st)
	}
}

// 截断越界：目标不在可见区间内，整体拒绝且状态不变。
func TestTruncateOutOfRange(t *testing.T) {
	l := openTemp(t)
	appendN(t, l, 5)
	if err := l.DeclareDurable(5); err != nil {
		t.Fatal(err)
	}
	if err := l.Truncate(3); err != nil {
		t.Fatal(err)
	}
	before := l.Status()

	if err := l.Truncate(2); !errors.Is(err, ErrTruncateOutOfRange) {
		t.Fatalf("Truncate(2) want ErrTruncateOutOfRange, got %v", err)
	}
	dump(t, l, "Truncate(2) rejected", "target=2 < start=4，前缀已删除")
	if err := l.Truncate(6); !errors.Is(err, ErrTruncateOutOfRange) {
		t.Fatalf("Truncate(6) want ErrTruncateOutOfRange, got %v", err)
	}
	dump(t, l, "Truncate(6) rejected", "target=6 > end=5，越界")
	if after := l.Status(); after != before {
		t.Fatalf("failed truncate changed state: before=%+v after=%+v", before, after)
	}
}

// 截断中途崩溃：标记已落盘但前缀未删，恢复时补删收敛。
func TestCrashBetweenMarkerAndDelete(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "log")
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	appendN(t, l, 10)
	if err := l.DeclareDurable(10); err != nil {
		t.Fatal(err)
	}
	l.SetCrashHook(l.InjectCrash)

	err = l.Truncate(6)
	if !errors.Is(err, ErrCrashed) {
		t.Fatalf("Truncate want ErrCrashed, got %v", err)
	}
	dump(t, l, "Truncate(6) crashed", "第一步标记=6 已落盘，第二步物理删除前崩溃")

	marker, _ := readMarker(dir)
	start, entries, _ := readLog(dir)
	t.Logf("crash site: marker=%d actualStart=%d entries=%d | 判定依据: start=%d <= marker=%d → 恢复时应补删收敛",
		marker, start, len(entries), start, marker)
	if marker != 6 || start != 1 || len(entries) != 10 {
		t.Fatalf("crash site wrong: marker=%d start=%d entries=%d", marker, start, len(entries))
	}

	l2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	dump(t, l2, "Open (recover)", "实际起始 1 < 标记 6 → 补删 (0,6] 收敛到 start=7")
	st := l2.Status()
	if st.Start != 7 || st.Entries != 4 || st.Marker != 6 {
		t.Fatalf("recovery did not converge: %+v", st)
	}
	if _, err := l2.ReadRange(1, 6); !errors.Is(err, ErrRangeUnavailable) {
		t.Fatalf("truncated prefix still visible: %v", err)
	}
	got, err := l2.ReadRange(7, 10)
	if err != nil || len(got) != 4 {
		t.Fatalf("ReadRange(7,10): %v len=%d", err, len(got))
	}
	dump(t, l2, "ReadRange(7,10)", "收敛后后缀连贯可读，前缀不可见")
}

// 干净恢复：标记与实际起始一致，不做任何收敛。
func TestCleanRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "log")
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	appendN(t, l, 8)
	if err := l.DeclareDurable(8); err != nil {
		t.Fatal(err)
	}
	if err := l.Truncate(5); err != nil {
		t.Fatal(err)
	}
	dump(t, l, "Truncate(5)", "两步完成：marker=5 start=6")

	l2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	dump(t, l2, "Open (recover)", "实际起始 6 == 标记 5 + 1 → 一致即干净")
	if st := l2.Status(); st.Start != 6 || st.Entries != 3 {
		t.Fatalf("clean recovery changed state: %+v", st)
	}
}

// 越删检测：实际起始越过标记，恢复整体拒绝并报告损坏。
func TestOverDeleteDetected(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "log")
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	appendN(t, l, 10)
	if err := l.DeclareDurable(10); err != nil {
		t.Fatal(err)
	}
	if err := l.Truncate(3); err != nil {
		t.Fatal(err)
	}
	dump(t, l, "Truncate(3)", "正常截断：marker=3 start=4")

	// 模拟外部越删：物理起始被推进到 8，但标记仍停在 3。
	if err := rewriteLog(dir, 8, []Entry{
		{Offset: 8, Data: []byte("x")},
		{Offset: 9, Data: []byte("x")},
		{Offset: 10, Data: []byte("x")},
	}); err != nil {
		t.Fatal(err)
	}
	marker, _ := readMarker(dir)
	t.Logf("tampered: marker=%d actualStart=8 | 判定依据: start=8 > marker=%d+1 → 越删，报告损坏", marker, marker)

	_, err = Open(dir)
	if !errors.Is(err, ErrOverDelete) {
		t.Fatalf("Open want ErrOverDelete, got %v", err)
	}
	t.Logf("op=%-28s err=%v", "Open (recover) rejected", err)
}

// 读/自检与追加、截断并发：读到的区间必须连贯，无中间态。
func TestConcurrentReadCheckWithAppendTruncate(t *testing.T) {
	l := openTemp(t)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // 追加 + 推进持久化位点
		defer wg.Done()
		for i := 0; i < 200; i++ {
			off, err := l.Append([]byte(fmt.Sprintf("v%d", i)))
			if err != nil {
				return
			}
			_ = l.DeclareDurable(off)
		}
	}()
	wg.Add(1)
	go func() { // 周期截断
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = l.ReclaimDurable()
			time.Sleep(time.Millisecond)
		}
	}()
	for i := 0; i < 100; i++ { // 并发读 + 自检
		st := l.Status()
		if st.Entries > 0 {
			to := st.End
			from := st.Start
			if st.Entries > 1 {
				from = st.Start + uint64(rand.Intn(st.Entries))
			}
			got, err := l.ReadRange(from, to)
			if err != nil {
				t.Fatalf("ReadRange(%d,%d): %v", from, to, err)
			}
			for j, e := range got {
				if want := from + uint64(j); e.Offset != want {
					t.Fatalf("non-contiguous read: index %d want %d got %d", j, want, e.Offset)
				}
			}
		}
		if _, err := l.Check(); err != nil {
			t.Fatalf("Check: %v", err)
		}
	}
	wg.Wait()
	dump(t, l, "concurrent mix done", "并发下读区间连贯、自检通过")
}

// 周期回收：reclaimer 自动把已持久化前缀截掉。
func TestPeriodicReclaim(t *testing.T) {
	l := openTemp(t)
	appendN(t, l, 20)
	if err := l.DeclareDurable(15); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := l.StartReclaimer(ctx, 5*time.Millisecond)
	defer func() { cancel(); stop() }()

	deadline := time.Now().Add(2 * time.Second)
	for l.Status().Start != 16 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	dump(t, l, "periodic reclaim", "reclaimer 截断到 durable=15 → start=16")
	if st := l.Status(); st.Start != 16 || st.Marker != 15 {
		t.Fatalf("reclaim did not happen: %+v", st)
	}
}

// 朴素模型核对：随机操作序列下，与“朴素追加再删除已截断前缀”的模型逐一比对。
func TestNaiveModelCrossCheck(t *testing.T) {
	l := openTemp(t)
	rng := rand.New(rand.NewSource(42))

	var model []uint64 // 模型中可见条目的偏移
	var next uint64 = 1
	var durable uint64

	for step := 0; step < 300; step++ {
		switch rng.Intn(4) {
		case 0, 1: // 追加
			off, err := l.Append([]byte(fmt.Sprintf("s%d", step)))
			if err != nil {
				t.Fatal(err)
			}
			if off != next {
				t.Fatalf("offset mismatch: want %d got %d", next, off)
			}
			model = append(model, next)
			next++
		case 2: // 声明持久化（合法前进）
			if next-1 > durable {
				durable = durable + 1 + uint64(rng.Intn(int(next-1-durable)+1))
				if durable > next-1 {
					durable = next - 1
				}
				if err := l.DeclareDurable(durable); err != nil {
					t.Fatalf("DeclareDurable(%d): %v", durable, err)
				}
			}
		case 3: // 截断到持久化位点
			if len(model) > 0 && durable >= model[0] {
				target := durable
				if target > model[len(model)-1] {
					target = model[len(model)-1]
				}
				if err := l.Truncate(target); err != nil {
					t.Fatalf("Truncate(%d): %v", target, err)
				}
				for len(model) > 0 && model[0] <= target { // 朴素删除已截断前缀
					model = model[1:]
				}
			}
		}

		st := l.Status()
		if st.Entries != len(model) {
			t.Fatalf("step %d: entries mismatch: log=%d model=%d", step, st.Entries, len(model))
		}
		if len(model) > 0 {
			if st.Start != model[0] {
				t.Fatalf("step %d: start mismatch: log=%d model=%d", step, st.Start, model[0])
			}
			got, err := l.ReadRange(model[0], model[len(model)-1])
			if err != nil {
				t.Fatalf("step %d: ReadRange: %v", step, err)
			}
			for j, e := range got {
				if e.Offset != model[j] {
					t.Fatalf("step %d: entry %d mismatch: log=%d model=%d", step, j, e.Offset, model[j])
				}
			}
		}
	}
	dump(t, l, "naive cross-check", "300 步随机操作与朴素模型完全一致")
}
