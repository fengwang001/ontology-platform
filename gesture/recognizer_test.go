package gesture

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func runLevels(t *testing.T, recognizer *Recognizer, levels []int) []Event {
	t.Helper()
	t.Logf("输入电平流: %v", levels)
	events, err := recognizer.Run(levels)
	if err != nil {
		t.Fatalf("Run(%v): %v", levels, err)
	}
	t.Logf("输出事件表: %v", events)
	return events
}

func assertEvents(t *testing.T, got, want []Event) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("event[%d] = %+v, want %+v; all got %v", i, got[i], want[i], got)
		}
	}

	var isPressed bool
	for _, event := range got {
		switch event.Kind {
		case Press:
			if isPressed {
				t.Fatalf("press/release not strictly alternating: %v", got)
			}
			isPressed = true
		case Release:
			if !isPressed {
				t.Fatalf("release without press: %v", got)
			}
			isPressed = false
		}
	}

	for i := 1; i < len(got); i++ {
		if got[i].Index < got[i-1].Index {
			t.Fatalf("event index decreased: %v", got)
		}
	}
}

func assertStatsIdentity(t *testing.T, recognizer *Recognizer) {
	t.Helper()

	stats := recognizer.Stats()
	rightSide := stats.SingleClicks + 2*stats.DoubleClicks + stats.CancelledClicks + stats.PendingClicks
	if stats.ShortPresses != rightSide {
		t.Fatalf("short press identity: %d != singles %d + 2*doubles %d + canceled %d + pending %d",
			stats.ShortPresses, stats.SingleClicks, stats.DoubleClicks, stats.CancelledClicks, stats.PendingClicks)
	}
	t.Logf("计数恒等式判定: 短按 %d = 单击 %d + 2×双击 %d + 作废 %d + 待定 %d",
		stats.ShortPresses, stats.SingleClicks, stats.DoubleClicks, stats.CancelledClicks, stats.PendingClicks)
}

func TestDebounceResetsWhenRawLevelReturnsToStable(t *testing.T) {
	t.Parallel()

	recognizer, err := New(3, 5, 5)
	if err != nil {
		t.Fatal(err)
	}

	t.Log("判定依据: 前两个 1 后出现 0，与稳定电平相同，因此消抖计数清零；重新连续三个 1 才按下")
	got := runLevels(t, recognizer, []int{1, 1, 0, 1, 1, 1, 1, 0})
	assertEvents(t, got, []Event{{Kind: Press, Index: 5}})
}

func TestDebounceAtThresholdAndOneBefore(t *testing.T) {
	t.Parallel()

	recognizer, err := New(3, 10, 10)
	if err != nil {
		t.Fatal(err)
	}

	got := runLevels(t, recognizer, []int{1, 1, 0, 1, 1})
	assertEvents(t, got, nil)

	got, err = recognizer.Sample(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入第三个连续 1，输出 %v；判定依据: 计数恰好达到 D=3", got)
	assertEvents(t, got, []Event{{Kind: Press, Index: 5}})
}

func TestShortPressAtLongPressThresholdMinusOne(t *testing.T) {
	t.Parallel()

	recognizer, err := New(1, 5, 10)
	if err != nil {
		t.Fatal(err)
	}

	levels := []int{1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	t.Log("判定依据: tp=0 tr=4，时长 4=L-1；单击裁决时刻为 tr+W+1=15")
	got := runLevels(t, recognizer, levels)
	want := []Event{
		{Kind: Press, Index: 0},
		{Kind: Release, Index: 4},
		{Kind: SingleClick, Index: 15},
	}
	assertEvents(t, got, want)
	assertStatsIdentity(t, recognizer)
}

func TestLongPressExactlyAtThresholdAndReleaseDoesNotClick(t *testing.T) {
	t.Parallel()

	recognizer, err := New(1, 5, 10)
	if err != nil {
		t.Fatal(err)
	}

	levels := []int{1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	t.Log("判定依据: 序号 5 同时满足 tp+L 和释放，先 LongPress 后 Release；时长不少于 L，不产生单击")
	got := runLevels(t, recognizer, levels)
	want := []Event{
		{Kind: Press, Index: 0},
		{Kind: LongPress, Index: 5},
		{Kind: Release, Index: 5},
	}
	assertEvents(t, got, want)
	assertStatsIdentity(t, recognizer)
}

func TestDoubleClickAtWindowBoundary(t *testing.T) {
	t.Parallel()

	recognizer, err := New(1, 5, 3)
	if err != nil {
		t.Fatal(err)
	}

	levels := []int{1, 0, 0, 0, 1, 0, 0}
	t.Log("判定依据: tp2-tr=4-1=3，含 W=3；第二次短按释放时 Release 先于 DoubleClick")
	got := runLevels(t, recognizer, levels)
	want := []Event{
		{Kind: Press, Index: 0},
		{Kind: Release, Index: 1},
		{Kind: Press, Index: 4},
		{Kind: Release, Index: 5},
		{Kind: DoubleClick, Index: 5},
	}
	assertEvents(t, got, want)
	assertStatsIdentity(t, recognizer)
}

func TestSingleClickWhenSecondPressIsOnePastWindow(t *testing.T) {
	t.Parallel()

	recognizer, err := New(1, 5, 3)
	if err != nil {
		t.Fatal(err)
	}

	levels := []int{1, 0, 0, 0, 0, 1, 0}
	t.Log("判定依据: tp2-tr=4=W+1；序号 5 先 SingleClick 后 Press，新按下重新作为第一击")
	got := runLevels(t, recognizer, levels)
	want := []Event{
		{Kind: Press, Index: 0},
		{Kind: Release, Index: 1},
		{Kind: SingleClick, Index: 5},
		{Kind: Press, Index: 5},
		{Kind: Release, Index: 6},
	}
	assertEvents(t, got, want)
	if stats := recognizer.Stats(); stats.PendingClicks != 1 {
		t.Fatalf("new press should start one pending click, got %+v", stats)
	}
	assertStatsIdentity(t, recognizer)
}

func TestZeroWindowNeverDoubleClicks(t *testing.T) {
	t.Parallel()

	recognizer, err := New(1, 5, 0)
	if err != nil {
		t.Fatal(err)
	}

	levels := []int{1, 0, 1, 0, 0}
	t.Log("判定依据: W=0 时任何下一次按下都至少晚 1 毫秒，因此不可能构成第二击")
	got := runLevels(t, recognizer, levels)
	want := []Event{
		{Kind: Press, Index: 0},
		{Kind: Release, Index: 1},
		{Kind: SingleClick, Index: 2},
		{Kind: Press, Index: 2},
		{Kind: Release, Index: 3},
		{Kind: SingleClick, Index: 4},
	}
	assertEvents(t, got, want)
}

func TestSecondClickBecomingLongPressCancelsFirst(t *testing.T) {
	t.Parallel()

	recognizer, err := New(1, 5, 10)
	if err != nil {
		t.Fatal(err)
	}

	levels := []int{1, 0, 1, 1, 1, 1, 1, 1, 0}
	t.Log("判定依据: 第二击在 tp2+L=7 成为长按，只产生 LongPress；第一次短按作废")
	got := runLevels(t, recognizer, levels)
	want := []Event{
		{Kind: Press, Index: 0},
		{Kind: Release, Index: 1},
		{Kind: Press, Index: 2},
		{Kind: LongPress, Index: 7},
		{Kind: Release, Index: 8},
	}
	assertEvents(t, got, want)
	stats := recognizer.Stats()
	if stats.ShortPresses != 1 || stats.CancelledClicks != 1 || stats.SingleClicks != 0 || stats.PendingClicks != 0 {
		t.Fatalf("unexpected cancellation stats: %+v", stats)
	}
	assertStatsIdentity(t, recognizer)
}

func TestThirdPressAfterDoubleClickRestartsFirstClick(t *testing.T) {
	t.Parallel()

	recognizer, err := New(1, 5, 10)
	if err != nil {
		t.Fatal(err)
	}

	levels := []int{1, 0, 1, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	t.Log("判定依据: 第二次短按已经组成双击并清空连击；序号 4 的按下重新计第一击")
	got := runLevels(t, recognizer, levels)
	want := []Event{
		{Kind: Press, Index: 0},
		{Kind: Release, Index: 1},
		{Kind: Press, Index: 2},
		{Kind: Release, Index: 3},
		{Kind: DoubleClick, Index: 3},
		{Kind: Press, Index: 4},
		{Kind: Release, Index: 5},
		{Kind: SingleClick, Index: 16},
	}
	assertEvents(t, got, want)
	assertStatsIdentity(t, recognizer)
}

func TestInvalidConstructionReasonsAreDistinguishable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		d    int
		l    int
		w    int
		want error
	}{
		{"debounce", 0, 1, 0, ErrInvalidDebounce},
		{"long press", 1, 0, 0, ErrInvalidLongPressThreshold},
		{"window", 1, 1, -1, ErrInvalidDoubleClickWindow},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(tc.d, tc.l, tc.w)
			if !errors.Is(err, tc.want) {
				t.Fatalf("New(%d,%d,%d) error = %v, want %v", tc.d, tc.l, tc.w, err, tc.want)
			}
		})
	}
}

func TestInvalidSampleDoesNotChangeState(t *testing.T) {
	t.Parallel()

	recognizer, err := New(2, 10, 10)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := recognizer.Sample(1); err != nil {
		t.Fatal(err)
	}
	_, err = recognizer.Sample(2)
	if !errors.Is(err, ErrInvalidLevel) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidLevel)
	}

	got, err := recognizer.Sample(0)
	if err != nil {
		t.Fatal(err)
	}
	assertEvents(t, got, nil)

	got, err = recognizer.Sample(1)
	if err != nil {
		t.Fatal(err)
	}
	assertEvents(t, got, nil)

	got, err = recognizer.Sample(1)
	if err != nil {
		t.Fatal(err)
	}
	assertEvents(t, got, []Event{{Kind: Press, Index: 3}})

	got, err = recognizer.Sample(1)
	if err != nil {
		t.Fatal(err)
	}
	assertEvents(t, got, nil)
}

func TestRunValidatesWholeBatchAtomically(t *testing.T) {
	t.Parallel()

	recognizer, err := New(1, 10, 10)
	if err != nil {
		t.Fatal(err)
	}

	before := recognizer.Stats()
	levels := []int{1, 0, 2}
	_, err = recognizer.Run(levels)
	if !errors.Is(err, ErrInvalidLevel) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidLevel)
	}
	after := recognizer.Stats()
	if before != after {
		t.Fatalf("rejected Run changed stats: before %+v after %+v", before, after)
	}

	got, err := recognizer.Run([]int{1})
	if err != nil {
		t.Fatal(err)
	}
	assertEvents(t, got, []Event{{Kind: Press, Index: 0}})
}

func TestRunIsIndependentOfPartitioning(t *testing.T) {
	t.Parallel()

	levels := []int{1, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

	one, err := New(1, 4, 3)
	if err != nil {
		t.Fatal(err)
	}
	all := runLevels(t, one, levels)

	other, err := New(1, 4, 3)
	if err != nil {
		t.Fatal(err)
	}
	partitions := [][]int{
		nil,
		{1, 0, 0},
		{0, 1, 0},
		{0, 0, 0, 0},
		{0, 0, 0, 0},
		{0, 0},
	}
	var split []Event
	for _, partition := range partitions {
		split = append(split, runLevels(t, other, partition)...)
	}

	assertEvents(t, split, all)
}

func TestConcurrentSamplesAreSerializable(t *testing.T) {
	t.Parallel()

	recognizer, err := New(10000, 100000, 100000)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 8
	const perWorker = 20
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(level int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if _, err := recognizer.Sample(level); err != nil {
					t.Errorf("Sample(%d): %v", level, err)
				}
			}
		}(worker % 2)
	}
	wg.Wait()

	stats := recognizer.Stats()
	if stats.ShortPresses+stats.SingleClicks+stats.DoubleClicks+stats.CancelledClicks+stats.PendingClicks != 0 {
		t.Fatalf("unexpected completed gestures: %+v", stats)
	}
}

func TestReplayProducesIdenticalEventTable(t *testing.T) {
	t.Parallel()

	levels := []int{1, 1, 0, 1, 1, 1, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0}
	first, err := New(3, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(3, 4, 2)
	if err != nil {
		t.Fatal(err)
	}

	assertEvents(t, runLevels(t, second, levels), runLevels(t, first, levels))
}

func ExampleRecognizer() {
	recognizer, _ := New(1, 3, 2)
	events, _ := recognizer.Run([]int{1, 1, 1, 0, 0, 0})
	for _, event := range events {
		fmt.Printf("%s@%d\n", event.Kind, event.Index)
	}
	// Output:
	// Press@0
	// LongPress@3
	// Release@3
}
