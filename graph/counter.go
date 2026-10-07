package graph

type resultCounter struct {
	accepted int
	limit    int
}

func newResultCounter(limit int) *resultCounter {
	return &resultCounter{limit: limit}
}

func (c *resultCounter) acceptOne() bool {
	if c.accepted >= c.limit {
		return false
	}
	c.accepted++
	return true
}

func (c *resultCounter) reached() bool {
	return c.accepted >= c.limit
}

func (c *resultCounter) count() int {
	return c.accepted
}
