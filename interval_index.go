package parking

import (
	"fmt"
	"sort"
)

type interval struct {
	start         Time
	end           Time
	reservationID string
}

type intervalNode struct {
	value  interval
	left   *intervalNode
	right  *intervalNode
	height int
}

type intervalTree struct {
	root *intervalNode
}

func newIntervalTree() *intervalTree {
	return &intervalTree{}
}

func (t *intervalTree) add(value interval) error {
	if value.end <= value.start {
		return fmt.Errorf("non-positive interval")
	}
	if existing := t.findAt(value.start); existing != nil || t.findOverlapping(value.start, value.end) != nil {
		return fmt.Errorf("interval overlaps existing occupancy")
	}
	t.root = insertInterval(t.root, value)
	return nil
}

func (t *intervalTree) findAt(at Time) *interval {
	node := t.root
	for node != nil {
		if at >= node.value.start && at < node.value.end {
			return &node.value
		}
		if at < node.value.start {
			node = node.left
		} else {
			node = node.right
		}
	}
	return nil
}

func (t *intervalTree) findOverlapping(start, end Time) *interval {
	return findOverlapping(t.root, start, end)
}

func (t *intervalTree) findOverlappingOther(start, end Time, excludedReservation string) *interval {
	return findOverlappingOther(t.root, start, end, excludedReservation)
}

func (t *intervalTree) firstOverlappingFrom(start, end Time, excludedReservation string) *interval {
	return firstOverlappingFrom(t.root, start, end, excludedReservation)
}

func (t *intervalTree) allOverlappingFrom(start, end Time, excludedReservation string) []*interval {
	var result []*interval
	collectOverlappingFrom(t.root, start, end, excludedReservation, &result)
	sort.Slice(result, func(i, j int) bool {
		if result[i].start != result[j].start {
			return result[i].start < result[j].start
		}
		return result[i].reservationID < result[j].reservationID
	})
	return result
}

func (t *intervalTree) firstStartInRange(at, before Time, excludedReservation string) *interval {
	result := firstStartInRange(t.root, at, before, excludedReservation)
	if result == nil {
		return nil
	}
	return result
}

func (t *intervalTree) removeStart(start Time) {
	t.root = removeInterval(t.root, start)
}

func (t *intervalTree) setInterval(start Time, value interval) {
	t.root = removeInterval(t.root, start)
	t.root = insertInterval(t.root, value)
}

func (t *intervalTree) addRaw(value interval) {
	t.root = insertInterval(t.root, value)
}

func insertInterval(node *intervalNode, value interval) *intervalNode {
	if node == nil {
		return &intervalNode{value: value, height: 1}
	}
	if value.start < node.value.start {
		node.left = insertInterval(node.left, value)
	} else {
		node.right = insertInterval(node.right, value)
	}
	return balanceInterval(node)
}

func findOverlapping(node *intervalNode, start, end Time) *interval {
	if node == nil {
		return nil
	}
	if start < node.value.end && node.value.start < end {
		return &node.value
	}
	if start < node.value.start {
		return findOverlapping(node.left, start, end)
	}
	return findOverlapping(node.right, start, end)
}

func findOverlappingOther(node *intervalNode, start, end Time, excludedReservation string) *interval {
	if node == nil {
		return nil
	}
	if start < node.value.end && node.value.start < end && node.value.reservationID != excludedReservation {
		return &node.value
	}
	if start < node.value.start {
		return findOverlappingOther(node.left, start, end, excludedReservation)
	}
	return findOverlappingOther(node.right, start, end, excludedReservation)
}

func firstOverlappingFrom(node *intervalNode, start, end Time, excludedReservation string) *interval {
	if node == nil {
		return nil
	}
	if candidate := firstOverlappingFrom(node.left, start, end, excludedReservation); candidate != nil {
		return candidate
	}
	if node.value.start < end && node.value.end > start && node.value.reservationID != excludedReservation {
		return &node.value
	}
	if node.value.start >= end {
		return nil
	}
	return firstOverlappingFrom(node.right, start, end, excludedReservation)
}

func collectOverlappingFrom(node *intervalNode, start, end Time, excludedReservation string, result *[]*interval) {
	if node == nil {
		return
	}
	collectOverlappingFrom(node.left, start, end, excludedReservation, result)
	if node.value.start < end && node.value.end > start && node.value.reservationID != excludedReservation {
		*result = append(*result, &node.value)
	}
	if node.value.start < end {
		collectOverlappingFrom(node.right, start, end, excludedReservation, result)
	}
}

func firstStartInRange(node *intervalNode, at, before Time, excludedReservation string) *interval {
	if node == nil {
		return nil
	}
	var candidate *interval
	if at <= node.value.start {
		candidate = firstStartInRange(node.left, at, before, excludedReservation)
	}
	if candidate != nil {
		return candidate
	}
	if node.value.start >= at && node.value.start < before && node.value.reservationID != excludedReservation {
		return &node.value
	}
	return firstStartInRange(node.right, at, before, excludedReservation)
}

func removeInterval(node *intervalNode, start Time) *intervalNode {
	if node == nil {
		return nil
	}
	if start < node.value.start {
		node.left = removeInterval(node.left, start)
	} else if start > node.value.start {
		node.right = removeInterval(node.right, start)
	} else {
		if node.left == nil {
			return node.right
		}
		if node.right == nil {
			return node.left
		}
		successor := minimumInterval(node.right)
		node.value = successor.value
		node.right = removeInterval(node.right, successor.value.start)
	}
	return balanceInterval(node)
}

func minimumInterval(node *intervalNode) *intervalNode {
	for node.left != nil {
		node = node.left
	}
	return node
}

func intervalHeight(node *intervalNode) int {
	if node == nil {
		return 0
	}
	return node.height
}

func balanceInterval(node *intervalNode) *intervalNode {
	node.height = 1 + max(intervalHeight(node.left), intervalHeight(node.right))
	balance := intervalHeight(node.left) - intervalHeight(node.right)
	if balance > 1 {
		if intervalHeight(node.left.left) < intervalHeight(node.left.right) {
			node.left = rotateIntervalLeft(node.left)
		}
		return rotateIntervalRight(node)
	}
	if balance < -1 {
		if intervalHeight(node.right.right) < intervalHeight(node.right.left) {
			node.right = rotateIntervalRight(node.right)
		}
		return rotateIntervalLeft(node)
	}
	return node
}

func rotateIntervalRight(node *intervalNode) *intervalNode {
	pivot := node.left
	node.left = pivot.right
	pivot.right = node
	node.height = 1 + max(intervalHeight(node.left), intervalHeight(node.right))
	pivot.height = 1 + max(intervalHeight(pivot.left), intervalHeight(pivot.right))
	return pivot
}

func rotateIntervalLeft(node *intervalNode) *intervalNode {
	pivot := node.right
	node.right = pivot.left
	pivot.left = node
	node.height = 1 + max(intervalHeight(node.left), intervalHeight(node.right))
	pivot.height = 1 + max(intervalHeight(pivot.left), intervalHeight(pivot.right))
	return pivot
}
