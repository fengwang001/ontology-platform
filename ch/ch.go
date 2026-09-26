// Package ch provides the per-row hash functions of the Count-Min Sketch.
// Row j (1-based) maps key x to column h_j(x) = (a_j*x + b_j) mod w.
package ch

// Col returns the column in row j (1-based) that key x hashes to,
// for a sketch of width w. Rows 1..3 use the fixed coefficients
// (1,0), (2,1), (5,3); rows j>=4 use a_j=2j-1, b_j=j.
func Col(j, w int, x int64) int {
	var a, b int64
	switch j {
	case 1:
		a, b = 1, 0
	case 2:
		a, b = 2, 1
	case 3:
		a, b = 5, 3
	default:
		a, b = int64(2*j-1), int64(j)
	}
	return int((a*x + b) % int64(w))
}
