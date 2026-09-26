// Package first is the first level of the two-level perfect hash:
// h1(x) = x mod m and the resulting bucket partition. It depends on nothing.
package first

// Bucket returns h1(x) = x mod m (mathematical mod, always in [0, m)).
func Bucket(x, m int) int {
	r := x % m
	if r < 0 {
		r += m
	}
	return r
}

// Partition groups keys into m buckets by h1; result[j] holds B_j.
func Partition(keys []int, m int) [][]int {
	buckets := make([][]int, m)
	for _, k := range keys {
		b := Bucket(k, m)
		buckets[b] = append(buckets[b], k)
	}
	return buckets
}
