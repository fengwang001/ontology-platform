package broadphase

import "sort"

type object struct {
	id    int64
	tight Box
	fat   Box
	layer uint16
	mask  uint16
}

func makePair(left, right int64) Pair {
	if left > right {
		left, right = right, left
	}
	return Pair{A: left, B: right}
}

func sortedPairs(pairs map[Pair]struct{}) []Pair {
	result := make([]Pair, 0, len(pairs))
	for pair := range pairs {
		result = append(result, pair)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].A != result[j].A {
			return result[i].A < result[j].A
		}
		return result[i].B < result[j].B
	})
	return result
}

func sortEventPairs(events Events) {
	sort.Slice(events.FatEnter, pairLess(events.FatEnter))
	sort.Slice(events.FatExit, pairLess(events.FatExit))
	sort.Slice(events.ContactEnter, pairLess(events.ContactEnter))
	sort.Slice(events.ContactExit, pairLess(events.ContactExit))
}

func pairLess(pairs []Pair) func(i, j int) bool {
	return func(i, j int) bool {
		if pairs[i].A != pairs[j].A {
			return pairs[i].A < pairs[j].A
		}
		return pairs[i].B < pairs[j].B
	}
}

func intersectsBox(left, right Box) bool {
	return left.LX < right.HX && right.LX < left.HX &&
		left.LY < right.HY && right.LY < left.HY
}

func intersectsBoxY(left, right Box) bool {
	return left.LY < right.HY && right.LY < left.HY
}

func expandBox(box Box, margin int64) Box {
	return Box{
		LX: box.LX - margin,
		HX: box.HX + margin,
		LY: box.LY - margin,
		HY: box.HY + margin,
	}
}

func boxContains(outer, inner Box) bool {
	return outer.LX <= inner.LX && inner.HX <= outer.HX &&
		outer.LY <= inner.LY && inner.HY <= outer.HY
}

func canCollide(left, right *object) bool {
	return left.layer&right.mask != 0 && right.layer&left.mask != 0
}

func validTightBox(box Box) bool {
	return box.LX < box.HX && box.LY < box.HY &&
		box.LX >= minCoordinate && box.HX <= maxCoordinate &&
		box.LY >= minCoordinate && box.HY <= maxCoordinate
}

func validFilter(layer, mask int) bool {
	return layer >= 0 && layer <= maxFilter && mask >= 0 && mask <= maxFilter
}

// Pairs returns all current collidable inflated-box intersections.
func (bp *Broadphase) Pairs() []Pair {
	bp.mu.RLock()
	defer bp.mu.RUnlock()
	return sortedPairs(bp.fat)
}

// Contacts returns all current collidable tight-box intersections.
func (bp *Broadphase) Contacts() []Pair {
	bp.mu.RLock()
	defer bp.mu.RUnlock()
	return sortedPairs(bp.contact)
}

// FatBox returns the current inflated box.
func (bp *Broadphase) FatBox(id int64) (Box, error) {
	bp.mu.RLock()
	defer bp.mu.RUnlock()
	current, ok := bp.objects[id]
	if !ok {
		return Box{}, ErrObjectNotFound
	}
	return current.fat, nil
}

// Tight returns the current tight box.
func (bp *Broadphase) Tight(id int64) (Box, error) {
	bp.mu.RLock()
	defer bp.mu.RUnlock()
	current, ok := bp.objects[id]
	if !ok {
		return Box{}, ErrObjectNotFound
	}
	return current.tight, nil
}

func (bp *Broadphase) crossedCount() int {
	bp.mu.RLock()
	defer bp.mu.RUnlock()
	return bp.crossed
}

func (bp *Broadphase) pairChecks() int {
	bp.mu.RLock()
	defer bp.mu.RUnlock()
	return bp.checks
}
