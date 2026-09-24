// Package kgrp implements the fixed key-group assignment rules:
// hash, key->group, group->instance and instance->range.
// It depends on no other package.
package kgrp

import "errors"

// Sentinel errors. The three failure classes are deliberately distinct.
var (
	ErrMaxPInvalid = errors.New("kgrp: maxP must be >= 1")
	ErrPInvalid    = errors.New("kgrp: parallelism p must satisfy 1 <= p <= maxP")
	ErrEmptyKey    = errors.New("kgrp: key must not be empty")
)

// Hash returns h(key): h starts at 0 and for every UTF-8 byte b of key,
// h = h*31 + b with natural uint32 overflow/wrap-around.
func Hash(key string) uint32 {
	var h uint32
	for i := 0; i < len(key); i++ {
		h = h*31 + uint32(key[i])
	}
	return h
}

// ValidMaxP reports whether maxP is a legal maximum parallelism.
func ValidMaxP(maxP int) bool { return maxP >= 1 }

// ValidP reports whether p is a legal parallelism for maxP.
func ValidP(p, maxP int) bool { return maxP >= 1 && p >= 1 && p <= maxP }

// KeyGroup maps key to a group in [0,maxP). It rejects a non-positive
// maxP and an empty key without touching any caller state.
func KeyGroup(key string, maxP int) (int, error) {
	if !ValidMaxP(maxP) {
		return 0, ErrMaxPInvalid
	}
	if key == "" {
		return 0, ErrEmptyKey
	}
	return int(Hash(key) % uint32(maxP)), nil
}

// Owner is inst(kg,p) = floor(kg*p/maxP). kg*p is computed in uint64 so it
// cannot overflow for any legal int inputs.
func Owner(kg, p, maxP int) int {
	return int(uint64(kg) * uint64(p) / uint64(maxP))
}

// Range returns the half-open group interval [start,end) owned by instance i
// at parallelism p: [ceil(i*maxP/p), ceil((i+1)*maxP/p)). It is exactly the
// run-length merge of the per-group formula Owner.
func Range(i, p, maxP int) (int, int) {
	start := (i*maxP + p - 1) / p
	end := ((i+1)*maxP + p - 1) / p
	return start, end
}

// Ranges returns the [start,end) pairs for instances 0..p-1. The intervals
// are disjoint, contiguous and partition [0,maxP); lengths differ by at most 1.
func Ranges(p, maxP int) ([][2]int, error) {
	if !ValidP(p, maxP) {
		return nil, ErrPInvalid
	}
	out := make([][2]int, p)
	for i := 0; i < p; i++ {
		s, e := Range(i, p, maxP)
		out[i] = [2]int{s, e}
	}
	return out, nil
}
