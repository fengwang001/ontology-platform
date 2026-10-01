package gesture

import "sync"

type EventKind string

const (
	Press       EventKind = "Press"
	Release     EventKind = "Release"
	LongPress   EventKind = "LongPress"
	SingleClick EventKind = "SingleClick"
	DoubleClick EventKind = "DoubleClick"
)

type Event struct {
	Kind  EventKind
	Index int
}

type Stats struct {
	ShortCount       int
	SingleClickCount int
	DoubleClickCount int
	DiscardedCount   int
	PendingCount     int
}

type GestureRecognizer struct {
	mu sync.Mutex

	d int
	l int
	w int

	nextIndex int
	stable    int
	changeRun int

	pressActive bool
	pressIndex  int
	longFired   bool

	awaitingSecond bool
	firstRelease   int
	singleDeadline int
	secondPress    bool

	totalShortCount int
	discardedCount  int
	pendingCount    int

	events []Event
}

func New(debounceCount, longPressThreshold, doubleClickWindow int) (*GestureRecognizer, error) {
	if debounceCount < 1 {
		return nil, ErrInvalidDebounceCount
	}
	if longPressThreshold < 1 {
		return nil, ErrInvalidLongPressThreshold
	}
	if doubleClickWindow < 0 {
		return nil, ErrInvalidDoubleClickWindow
	}

	return &GestureRecognizer{
		d: debounceCount,
		l: longPressThreshold,
		w: doubleClickWindow,
	}, nil
}

func (g *GestureRecognizer) Sample(level int) ([]Event, error) {
	if level != 0 && level != 1 {
		return nil, ErrInvalidLevel
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	index := g.nextIndex
	g.nextIndex++
	return g.sampleLocked(level, index), nil
}

func (g *GestureRecognizer) Run(levels []int) ([]Event, error) {
	for _, level := range levels {
		if level != 0 && level != 1 {
			return nil, ErrInvalidLevel
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	newEvents := make([]Event, 0, len(levels)*3)
	for _, level := range levels {
		index := g.nextIndex
		g.nextIndex++
		newEvents = append(newEvents, g.sampleLocked(level, index)...)
	}
	return newEvents, nil
}

func (g *GestureRecognizer) Events() []Event {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Event(nil), g.events...)
}

func (g *GestureRecognizer) Stats() Stats {
	g.mu.Lock()
	defer g.mu.Unlock()

	return Stats{
		ShortCount:       g.totalShortCount,
		SingleClickCount: g.countLocked(SingleClick),
		DoubleClickCount: g.countLocked(DoubleClick),
		DiscardedCount:   g.discardedCount,
		PendingCount:     g.pendingCount,
	}
}

func (g *GestureRecognizer) sampleLocked(level int, index int) []Event {
	events := make([]Event, 0, 3)
	emit := func(kind EventKind) {
		event := Event{Kind: kind, Index: index}
		events = append(events, event)
		g.events = append(g.events, event)
	}

	if g.awaitingSecond && index == g.singleDeadline {
		emit(SingleClick)
		g.pendingCount = 0
		g.awaitingSecond = false
	}

	if g.pressActive && !g.longFired && index >= g.pressIndex+g.l {
		emit(LongPress)
		g.longFired = true
		if g.secondPress {
			g.awaitingSecond = false
			g.discardedCount++
			g.pendingCount = 0
			g.secondPress = false
		}
	}

	if level != g.stable {
		g.changeRun++
		if g.changeRun == g.d {
			g.stable ^= 1
			g.changeRun = 0

			if g.stable == 1 {
				emit(Press)
				if g.awaitingSecond && index-g.firstRelease <= g.w {
					g.secondPress = true
					g.awaitingSecond = false
				}

				g.pressActive = true
				g.pressIndex = index
				g.longFired = false
			} else {
				emit(Release)
				g.pressActive = false
				if g.longFired {
					g.longFired = false
				} else {
					g.totalShortCount++
					if g.secondPress {
						emit(DoubleClick)
						g.pendingCount = 0
						g.secondPress = false
					} else {
						g.awaitingSecond = true
						g.firstRelease = index
						g.singleDeadline = index + g.w + 1
						g.pendingCount = 1
					}
				}
			}
		}
	} else {
		g.changeRun = 0
	}

	return events
}

func (g *GestureRecognizer) countLocked(kind EventKind) int {
	count := 0
	for _, event := range g.events {
		if event.Kind == kind {
			count++
		}
	}
	return count
}
