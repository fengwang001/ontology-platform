// Package first implements the FKS first level: h1(x)=x mod m and bucketing.
package first

// H1 returns the bucket index of x: h1(x) = x mod m.
// Mathematical modulo: the result is always in [0, m).
func H1(x, m int) int {
	r := x % m
	if r < 0 {
		r += m
	}
	return r
}

// Partition groups keys into m buckets according to H1, preserving input
// order inside each bucket. The caller must guarantee m >= 1.
func Partition(keys []int, m int) [][]int {
	buckets := make([][]int, m)
	for _, k := range keys {
		b := H1(k, m)
		buckets[b] = append(buckets[b], k)
	}
	return buckets
}
