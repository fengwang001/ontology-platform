// Package tbucket computes time-bucket keys with floor division.
package tbucket

// FloorDiv returns floor(a/b) for b > 0, correct for negative a
// (Go's / truncates toward zero, which is wrong here).
func FloorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

// Key returns the key of the bucket containing ts for bucket width size.
func Key(ts, size int64) int64 { return FloorDiv(ts, size) }

// Contains reports whether ts lies in bucket k's half-open
// interval [k*size, (k+1)*size).
func Contains(ts, k, size int64) bool { return Key(ts, size) == k }
