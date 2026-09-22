// Package scan implements the predicate push-down scanner: zone-map
// pruning, selective decoding and cursor-based continuation.
package scan

import "ontology/segment"

// Cursor is an opaque resumption token produced by a scan.
type Cursor struct {
	data []byte
}

// Scanner scans one read-only segment. A Scanner is not safe for
// concurrent use by itself, but many scanners may share one segment.
type Scanner struct{}

// DecodeStats are internal counters proving pruning really happened.
type DecodeStats struct {
	GroupsDecoded int
	ValuesDecoded int
}

// New constructs a Scanner over seg.
func New(seg *segment.Segment) *Scanner { return nil }
