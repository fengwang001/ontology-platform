package gesture

import (
	"fmt"
	"math/rand"
	"testing"
)

type naiveRecognizer struct {
	d         int
	l         int
	w         int
	stable    int
	diffRun   int
	pressed   bool
	pressAt   int
	longDone  bool
	pending   bool
	second    bool
	releaseAt int
	short     int
	singles   int
	doubles   int
	canceled  int
}

func newNaive(d, l, w int) *naiveRecognizer {
	return &naiveRecognizer{d: d, l: l, w: w}
}

func (n *naiveRecognizer) sample(index, level int) []Event {
	events := make([]Event, 0, 3)

	if n.pressed && !n.longDone && index == n.pressAt+n.l {
		events = append(events, Event{Kind: LongPress, Index: index})
		n.longDone = true
		if n.second {
			n.pending = false
			n.second = false
			n.canceled++
		}
	}

	if n.pending && !n.second && index == n.releaseAt+n.w+1 {
		events = append(events, Event{Kind: SingleClick, Index: index})
		n.pending = false
		n.singles++
	}

	if level == n.stable {
		n.diffRun = 0
		return events
	}

	n.diffRun++
	if n.diffRun < n.d {
		return events
	}

	n.stable ^= 1
	n.diffRun = 0

	if n.stable == 1 {
		n.pressed = true
		n.pressAt = index
		n.longDone = false

		isSecond := n.pending && index-n.releaseAt <= n.w
		n.second = isSecond
		if !isSecond {
			n.pending = false
		}
		events = append(events, Event{Kind: Press, Index: index})
		return events
	}

	n.pressed = false
	n.longDone = false
	n.second = false
	shortPress := index-n.pressAt < n.l
	events = append(events, Event{Kind: Release, Index: index})

	if !shortPress {
		n.pending = false
		return events
	}

	n.short++
	if n.pending {
		events = append(events, Event{Kind: DoubleClick, Index: index})
		n.pending = false
		n.doubles++
		return events
	}

	n.pending = true
	n.releaseAt = index
	return events
}

func TestMatchesStepByStepNaiveModel(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(20261001))
	for sequence := 0; sequence < 80; sequence++ {
		d := 1 + rng.Intn(3)
		l := 1 + rng.Intn(6)
		w := rng.Intn(5)
		levels := make([]int, 90)
		for i := range levels {
			levels[i] = rng.Intn(2)
		}

		t.Run(fmt.Sprintf("seq%d_D%d_L%d_W%d", sequence, d, l, w), func(t *testing.T) {
			recognizer, err := New(d, l, w)
			if err != nil {
				t.Fatal(err)
			}
			naive := newNaive(d, l, w)

			var actualAll []Event
			var expectedAll []Event
			for index, level := range levels {
				reason := naive.reason(index, level)
				actual, err := recognizer.Sample(level)
				if err != nil {
					t.Fatalf("index %d level %d: %v", index, level, err)
				}
				expected := naive.sample(index, level)
				t.Logf("序号 %2d 输入=%d 输出=%v 朴素输出=%v 判定依据=%s",
					index, level, actual, expected, reason)
				assertEqualEvents(t, actual, expected)
				actualAll = append(actualAll, actual...)
				expectedAll = append(expectedAll, expected...)
			}

			assertEvents(t, actualAll, expectedAll)
			stats := recognizer.Stats()
			pending := 0
			if naive.pending {
				pending = 1
			}
			if stats.ShortPresses != naive.short ||
				stats.SingleClicks != naive.singles ||
				stats.DoubleClicks != naive.doubles ||
				stats.CancelledClicks != naive.canceled ||
				stats.PendingClicks != pending {
				t.Fatalf("stats = %+v, want short=%d singles=%d doubles=%d canceled=%d pending=%d",
					stats, naive.short, naive.singles, naive.doubles, naive.canceled, pending)
			}
			assertStatsIdentity(t, recognizer)
		})
	}
}

func assertEqualEvents(t *testing.T, got, want []Event) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("event[%d] = %+v, want %+v; all got %v", i, got[i], want[i], got)
		}
	}
}

func (n *naiveRecognizer) reason(index, level int) string {
	switch {
	case n.pressed && !n.longDone && index == n.pressAt+n.l:
		return "到点长按优先"
	case n.pending && !n.second && index == n.releaseAt+n.w+1:
		return "第一击超时，裁决单击"
	case level == n.stable:
		return "原始电平等于稳定电平，消抖计数清零"
	case n.diffRun+1 < n.d:
		return "异电平连续计数尚未达到 D"
	case level == 1:
		return "异电平连续计数达到 D，稳定按下；到点事件已先处理"
	default:
		return "消抖计数达到 D，稳定释放"
	}
}
