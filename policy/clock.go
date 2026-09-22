package policy

// Clock abstracts time so production code never calls the time package
// directly. Sleep returns false when the context signal fires.
type Clock interface {
	NowMillis() int64
	Sleep(millis int64) bool
}

// ManualClock is a deterministic test clock. Sleep blocks until Advance
// releases enough virtual time; Advance itself runs in a goroutine-free
// cooperative model driven by the test.
type ManualClock struct {
	now      int64
	deadline []int64
	wake     []chan struct{}
}

// NewManualClock returns a clock at virtual time 0.
func NewManualClock() *ManualClock { return &ManualClock{} }

// NowMillis returns virtual elapsed milliseconds.
func (c *ManualClock) NowMillis() int64 { return c.now }

// Sleep blocks until virtual time advances past the deadline.
func (c *ManualClock) Sleep(millis int64) bool {
	if millis <= 0 {
		return true
	}
	wake := make(chan struct{})
	c.deadline = append(c.deadline, c.now+millis)
	c.wake = append(c.wake, wake)
	<-wake
	return true
}

// Advance moves virtual time by millis and releases due sleepers.
func (c *ManualClock) Advance(millis int64) {
	c.now += millis
	var keptD []int64
	var keptW []chan struct{}
	for i, d := range c.deadline {
		if c.now >= d {
			close(c.wake[i])
		} else {
			keptD = append(keptD, d)
			keptW = append(keptW, c.wake[i])
		}
	}
	c.deadline, c.wake = keptD, keptW
}

// Pending returns the number of sleepers still blocked.
func (c *ManualClock) Pending() int { return len(c.deadline) }
