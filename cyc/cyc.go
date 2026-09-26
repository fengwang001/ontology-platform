// Package cyc implements the lexicographically minimal cyclic rotation
// (Booth's two-pointer algorithm) over byte strings.
// It depends on no other package in this module.
package cyc

// MinRotation returns the starting index k (0 <= k < len(s)) of the
// lexicographically smallest rotation of s. When several rotations are
// equal (s is periodic), the smallest such k is returned.
//
// The empty string has no rotation; 0 is returned as a defensive value
// (the api package rejects empty strings before reaching here).
func MinRotation(s string) int {
	idx, _ := minRotationCount(s)
	return idx
}

// Rotate returns the rotation s[k:] + s[:k]. It panics when k is out of
// range, mirroring Go slice semantics; callers that need a reportable
// error validate k first (see package api).
func Rotate(s string, k int) string {
	return s[k:] + s[:k]
}

// boothState holds one Booth run. All fields are unexported; compares is
// the character-comparison counter required for the O(n) proof. It is
// reachable only from inside this package (including its tests), never
// through an exported function or method.
type boothState struct {
	s        string
	n        int
	compares int
}

// run executes Booth's two-pointer scan over d = s+s.
//
// Invariant per step: rotations i and j are the two surviving candidates;
// their first k characters already compare equal. On a mismatch the
// losing candidate (and every index skipped while k grew) is eliminated
// in one jump, which is what keeps the total character comparisons linear.
// On a full run of equal characters (k == n), min(i, j) is returned,
// which yields the smallest starting index for periodic strings.
func (b *boothState) run() int {
	n := b.n
	if n == 0 {
		return 0
	}
	d := b.s + b.s
	i, j, k := 0, 1, 0
	for i < n && j < n && k < n {
		ci, cj := d[i+k], d[j+k]
		b.compares++
		if ci == cj {
			k++
			continue
		}
		if ci > cj { // rotation i loses
			i += k + 1
			if i <= j { // candidates must stay distinct
				i = j + 1
			}
		} else { // rotation j loses
			j += k + 1
			if j <= i {
				j = i + 1
			}
		}
		k = 0
	}
	if i < j {
		return i
	}
	return j
}

// minRotationCount runs Booth and also returns the number of character
// pairs actually compared. Unexported: the complexity test lives in this
// package and reads the counter directly; the counter is never exposed.
func minRotationCount(s string) (idx, compares int) {
	b := boothState{s: s, n: len(s)}
	return b.run(), b.compares
}
