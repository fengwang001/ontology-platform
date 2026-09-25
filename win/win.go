// Package win owns the pure arithmetic of the sliding window:
// the half-open interval (T-W, T] and bucket index TS/b.
// It depends on no other package.
package win

// InWindow reports whether ts lies in the left-open, right-closed
// window (T-W, T]. A timestamp exactly on T-W is excluded; one
// exactly on T is included.
func InWindow(ts, T, W int64) bool {
	return ts > T-W && ts <= T
}

// Expired reports whether ts is at or left of the open left edge,
// i.e. ts <= T-W. Such a timestamp can never belong to the window.
func Expired(ts, T, W int64) bool {
	return ts <= T-W
}

// Bucket returns the bucket index a non-negative timestamp belongs
// to: idx = ts / b (integer division).
func Bucket(ts, b int64) int64 {
	return ts / b
}

// BucketEnd returns the exclusive right edge of bucket idx:
// the half-open interval [idx*b, (idx+1)*b).
func BucketEnd(idx, b int64) int64 {
	return (idx + 1) * b
}

// MaxExpiredBucket returns the largest bucket index whose WHOLE
// interval is at or left of T-W, i.e. (idx+1)*b <= T-W. Every key
// in buckets [0, idx] is certainly expired. It returns -1 when no
// bucket qualifies yet (including while T-W < 0).
func MaxExpiredBucket(T, W, b int64) int64 {
	cutoff := T - W
	if cutoff < b {
		return -1
	}
	return cutoff/b - 1
}
