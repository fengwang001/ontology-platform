package gesture

import "testing"

type naiveEvent struct {
	kind  EventKind
	index int
}

func naiveRun(t *testing.T, d, l, w int, levels []int) []naiveEvent {
	t.Helper()

	stable := 0
	run := 0
	pressed := false
	longFired := false
	pressAt := 0
	waiting := false
	waitingRelease := 0
	second := false
	events := make([]naiveEvent, 0)

	emit := func(kind EventKind, index int, reason string) {
		events = append(events, naiveEvent{kind: kind, index: index})
		t.Logf("naive index=%d output=%s decision=%s", index, kind, reason)
	}

	for index, level := range levels {
		t.Logf("naive index=%d input=%d stable=%d run=%d waiting=%v second=%v", index, level, stable, run, waiting, second)

		if waiting && index == waitingRelease+w+1 {
			emit(SingleClick, index, "first short press reached single-click deadline")
			waiting = false
		}

		if pressed && !longFired && index >= pressAt+l {
			emit(LongPress, index, "stable press reached long-press threshold")
			longFired = true
			if second {
				t.Logf("naive index=%d decision=discard first short press because second became long", index)
				waiting = false
				second = false
			}
		}

		if level != stable {
			run++
			if run == d {
				stable = level
				run = 0

				if stable == 1 {
					emit(Press, index, "different raw level count reached debounce count")
					if waiting && index-waitingRelease <= w {
						second = true
						waiting = false
						t.Logf("naive index=%d decision=second click starts because gap=%d <= W=%d", index, index-waitingRelease, w)
					}
					waiting = false
					pressed = true
					pressAt = index
					longFired = false
				} else {
					emit(Release, index, "different raw level count reached debounce count")
					pressed = false
					if longFired {
						t.Logf("naive index=%d decision=long-press release emits no click", index)
						longFired = false
					} else if second {
						emit(DoubleClick, index, "second click also ended as a short press")
						second = false
					} else {
						t.Logf("naive index=%d decision=short press duration=%d < L=%d; await second click", index, index-pressAt, l)
						waiting = true
						waitingRelease = index
					}
				}
			} else {
				t.Logf("naive index=%d decision=debounce run=%d, below D=%d", index, run, d)
			}
		} else {
			if run != 0 {
				t.Logf("naive index=%d decision=raw level matched stable; reset debounce run from %d", index, run)
			}
			run = 0
		}
	}

	return events
}
