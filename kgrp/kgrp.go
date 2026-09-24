// Package kgrp maps keys to key groups and key groups to operator instances.
// It has no dependencies on the other packages of this module.
package kgrp

import "errors"

// Sentinel errors. The three rejection causes are deliberately distinct so
// callers can branch on errors.Is.
var (
	ErrInvalidMaxP        = errors.New("kgrp: maxP must be >= 1")
	ErrInvalidParallelism = errors.New("kgrp: parallelism must be in [1, maxP]")
	ErrEmptyKey           = errors.New("kgrp: key must not be empty")
)

// Hash computes h = 0 then h = h*31 + b over the UTF-8 bytes of key,
// with natural uint32 wraparound.
func Hash(key string) uint32 {
	var h uint32
	for i := 0; i < len(key); i++ {
		h = h*31 + uint32(key[i])
	}
	return h
}

// CheckMaxP validates the maximum parallelism.
func CheckMaxP(maxP int) error {
	if maxP < 1 {
		return ErrInvalidMaxP
	}
	return nil
}

// CheckP validates 1 <= p <= maxP.
func CheckP(maxP, p int) error {
	if maxP < 1 {
		return ErrInvalidMaxP
	}
	if p < 1 || p > maxP {
		return ErrInvalidParallelism
	}
	return nil
}

// KeyGroup returns h(key) mod maxP.
func KeyGroup(key string, maxP int) (int, error) {
	if err := CheckMaxP(maxP); err != nil {
		return 0, err
	}
	if key == "" {
		return 0, ErrEmptyKey
	}
	return int(Hash(key) % uint32(maxP)), nil
}

// Instance returns inst(kg, p) = floor(kg * p / maxP). The product is
// evaluated in int64 so it cannot overflow for any realistic key-group count.
// Callers must validate maxP and p beforehand via CheckP.
func Instance(kg, p, maxP int) int {
	return int(int64(kg) * int64(p) / int64(maxP))
}

// RangeStart returns ceil(i * maxP / p), the inclusive start of the key
// group range owned by instance i. Callers must validate inputs via CheckP.
func RangeStart(i, p, maxP int) int {
	return int((int64(i)*int64(maxP) + int64(p) - 1) / int64(p))
}

// RangeBounds returns the half-open range [start, end) of key groups owned
// by instance i: [ceil(i*maxP/p), ceil((i+1)*maxP/p)).
func RangeBounds(i, p, maxP int) (start, end int) {
	return RangeStart(i, p, maxP), RangeStart(i+1, p, maxP)
}
