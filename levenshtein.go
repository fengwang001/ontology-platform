package ontology

// Options tunes the comparison.
type Options struct {
	// FoldCase compares runes up to Unicode simple case folding
	// (unicode.SimpleFold orbits). It is a 1:1 rune mapping: "straße"
	// matches "STRASSE", but full-fold expansions such as "Straße" vs
	// "STRASSE" (ß -> ss) are NOT equated.
	FoldCase bool
}

// Stats exposes observability for a single comparison.
type Stats struct {
	// CellsFilled is the number of DP cells evaluated. It never exceeds
	// (2k+1)*(min(lenA,lenB)+1) and is smaller when early termination
	// fires or near the matrix corners.
	CellsFilled int64
	// MaxWorkArrayLen is the length of each rolling work array:
	// min(2k+1, min(lenA,lenB)+1).
	MaxWorkArrayLen int
}

// Result is the outcome of one thresholded comparison.
type Result struct {
	// Distance is the exact Levenshtein distance in code points.
	// It is meaningful only when Exceeded is false.
	Distance int
	// Exceeded reports that the distance is greater than k. In that case
	// the computation terminated early and Distance is undefined.
	Exceeded bool
	Stats    Stats
}

// Distance compares a and b with threshold k using default options.
func Distance(a, b string, k int) (Result, error) {
	return DistanceWithOptions(a, b, k, Options{})
}

// DistanceWithOptions compares a and b with threshold k. If the rune
// distance is at most k it returns the exact distance; otherwise it stops
// early and reports Exceeded. A negative k yields ErrNegativeK; invalid
// UTF-8 yields a *UTF8Error naming the side and byte offset.
func DistanceWithOptions(a, b string, k int, opts Options) (Result, error) {
	if k < 0 {
		return Result{}, ErrNegativeK
	}
	ra, err := decodeRunes(a, SideLeft)
	if err != nil {
		return Result{}, err
	}
	rb, err := decodeRunes(b, SideRight)
	if err != nil {
		return Result{}, err
	}
	if opts.FoldCase {
		foldRunes(ra)
		foldRunes(rb)
	}
	return banded(ra, rb, k), nil
}

// banded evaluates the Levenshtein DP restricted to the diagonal band
// |i-j| <= k, which is the only region that can contain values <= k.
// The shorter rune slice is laid along the rolling-array axis, so memory
// is min(2k+1, minLen+1) integers. The band is symmetric under
// transposition, so Distance(a,b) and Distance(b,a) fill the same number
// of cells.
func banded(ra, rb []rune, k int) Result {
	m, n := len(ra), len(rb)
	diff := m - n
	if diff < 0 {
		diff = -diff
	}
	if diff > k {
		// Length difference alone exceeds k: zero cells filled.
		return Result{Exceeded: true}
	}
	if n > m {
		ra, rb = rb, ra
		m, n = n, m
	}
	width := 2*k + 1
	if n+1 < width {
		width = n + 1
	}
	inf := m + n + 1 // larger than any real distance; safe against +1 overflow
	prev := make([]int, width)
	cur := make([]int, width)
	var cells int64

	// Row 0: D[0][j] = j for j in [0, min(k, n)].
	hi0 := min(k, n)
	for j := 0; j <= hi0; j++ {
		prev[j] = j
	}
	cells += int64(hi0 + 1)
	lastLo := 0

	for i := 1; i <= m; i++ {
		lo := max(i-k, 0)
		hi := min(i+k, n)
		plo := max(i-1-k, 0) // previous row's window start
		phi := min(i-1+k, n) // previous row's window end
		rowMin := inf
		for j := lo; j <= hi; j++ {
			best := inf
			if j >= plo && j <= phi { // deletion: D[i-1][j] + 1
				if v := prev[j-plo] + 1; v < best {
					best = v
				}
			}
			if j-1 >= lo { // insertion: D[i][j-1] + 1
				if v := cur[j-1-lo] + 1; v < best {
					best = v
				}
			}
			if j-1 >= plo && j-1 <= phi { // substitution: D[i-1][j-1] + cost
				cost := 0
				if ra[i-1] != rb[j-1] {
					cost = 1
				}
				if v := prev[j-1-plo] + cost; v < best {
					best = v
				}
			}
			cur[j-lo] = best
			if best < rowMin {
				rowMin = best
			}
		}
		cells += int64(hi - lo + 1)
		lastLo = lo
		if rowMin > k {
			// DP values are non-decreasing along diagonals, so no
			// later row can come back under k: stop early.
			return Result{Exceeded: true, Stats: Stats{CellsFilled: cells, MaxWorkArrayLen: width}}
		}
		prev, cur = cur, prev
	}
	return Result{
		Distance: prev[n-lastLo],
		Stats:    Stats{CellsFilled: cells, MaxWorkArrayLen: width},
	}
}
