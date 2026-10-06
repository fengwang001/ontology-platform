package ontology

import "sort"

type TemporaryClosure struct {
	ID           string
	Start        int64
	PlannedEnd   int64
	ActualEnd    int64
	Ended        bool
	CancelOrders bool
}

type closureState struct {
	temporary  []*TemporaryClosure
	cursor     int
	forced     bool
	forceStart int64
}

func (c *closureState) add(closure *TemporaryClosure, at, maxDuration, minGap int64) error {
	if closure.Start < at {
		return &CallError{Code: ErrInvalidParameter, Message: "temporary closure cannot start in the past"}
	}
	duration := closure.PlannedEnd - closure.Start
	if duration <= 0 || duration > maxDuration {
		return &CallError{Code: ErrInvalidParameter, Message: "temporary closure duration is outside the allowed range"}
	}
	index := sort.Search(len(c.temporary), func(i int) bool {
		return c.temporary[i].Start >= closure.Start
	})
	if index < len(c.temporary) {
		next := c.temporary[index]
		if next.Start == closure.Start || (!next.Ended && next.Start < closure.PlannedEnd) {
			return &CallError{Code: ErrClosureOverlap, Message: "temporary closure overlaps an existing closure"}
		}
	}
	if index > 0 {
		previous := c.temporary[index-1]
		if !previous.Ended {
			if closure.Start < previous.PlannedEnd {
				return &CallError{Code: ErrClosureOverlap, Message: "temporary closure overlaps an existing closure"}
			}
		} else if closure.Start-previous.ActualEnd < minGap {
			return &CallError{Code: ErrClosureGap, Message: "temporary closures are too close"}
		}
	}
	c.temporary = append(c.temporary, nil)
	copy(c.temporary[index+1:], c.temporary[index:])
	c.temporary[index] = closure
	if index < c.cursor {
		c.cursor = index
	}
	return nil
}

func (c *closureState) end(id string, at int64) (*TemporaryClosure, error) {
	for _, closure := range c.temporary {
		if closure.ID != id {
			continue
		}
		if closure.Ended {
			return nil, &CallError{Code: ErrInvalidState, Message: "temporary closure already ended"}
		}
		if at < closure.Start {
			return nil, &CallError{Code: ErrInvalidState, Message: "temporary closure has not started"}
		}
		if at >= closure.PlannedEnd {
			return nil, &CallError{Code: ErrInvalidState, Message: "temporary closure already ended"}
		}
		closure.Ended = true
		closure.ActualEnd = at
		return closure, nil
	}
	return nil, &CallError{Code: ErrNotFound, Message: "temporary closure not found"}
}

func (c *closureState) activeAt(at int64) bool {
	for c.cursor < len(c.temporary) && c.temporary[c.cursor].Ended && c.temporary[c.cursor].ActualEnd <= at {
		c.cursor++
	}
	if c.cursor < len(c.temporary) {
		closure := c.temporary[c.cursor]
		return closure.Start <= at && (!closure.Ended || closure.ActualEnd > at)
	}
	return false
}

func (c *closureState) contains(at int64) bool {
	index := sort.Search(len(c.temporary), func(i int) bool {
		return c.temporary[i].Start > at
	}) - 1
	if index < 0 {
		return false
	}
	closure := c.temporary[index]
	end := closure.PlannedEnd
	if closure.Ended {
		end = closure.ActualEnd
	}
	return closure.Start <= at && at < end
}

func (c *closureState) startForce(at int64) error {
	if c.forced {
		return &CallError{Code: ErrInvalidState, Message: "merchant is already force-closed"}
	}
	c.forced = true
	c.forceStart = at
	return nil
}

func (c *closureState) liftForce() error {
	if !c.forced {
		return &CallError{Code: ErrInvalidState, Message: "merchant is not force-closed"}
	}
	c.forced = false
	c.forceStart = 0
	return nil
}
