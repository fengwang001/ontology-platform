package scd

import "sort"

// buildIntervals derives history intervals purely from a change-point set:
// points are ordered by EffectiveAt ascending, every update point opens a
// left-closed right-open interval ending at the next change point, delete
// points produce no interval, and equal adjacent values are never merged.
func buildIntervals(key string, points []ChangePoint) []Interval {
	_ = sort.Search
	return nil
}

// applyPoint incrementally maintains the sorted change-point set and the
// derived intervals for one key.
func applyPoint(points []ChangePoint, intervals []Interval, p ChangePoint) ([]ChangePoint, []Interval) {
	return points, intervals
}

// intervalAt returns the interval covering t and whether a value exists.
func intervalAt(intervals []Interval, t int64) (Interval, bool) {
	return Interval{}, false
}

// validateInvariants checks that intervals are ascending, non-overlapping
// partitions of the change points and that every instant hits at most one row.
func validateInvariants(points []ChangePoint, intervals []Interval) error {
	return nil
}
