package ontology

// Stats exposes how much work a single Compare call actually did.
type Stats struct {
	// Cells is the number of DP cells computed (boundary row/column
	// initialization is not counted).
	Cells int
	// MaxWorkLen is the total length, in ints, of the working arrays
	// allocated for the comparison (two rolling rows).
	MaxWorkLen int
}

// Result is the outcome of a thresholded comparison. When Exceeded is
// true the distance is known to be greater than k and Distance is
// meaningless; otherwise Distance is the exact Levenshtein distance.
type Result struct {
	Exceeded bool
	Distance int
	Stats    Stats
}

// Matcher compares strings under a Levenshtein threshold. The zero value
// is ready to use and compares runes exactly.
type Matcher struct {
	// FoldCase enables Unicode simple case folding (per-rune, via
	// unicode.SimpleFold). Full folding such as 'ß' -> "ss" is not
	// performed, so "Straße" vs "STRASSE" still differs.
	FoldCase bool
}

// Compare reports whether the Levenshtein distance between a and b is at
// most k. Distance is measured in runes, not bytes.
//
// The algorithm is a banded (Ukkonen) DP: only cells with |i-j| <= k are
// computed, so the work is O(k*min(len)) cells and O(min(len)) memory,
// and a row whose band values all exceed k aborts the scan early. It
// never computes the full distance and then compares against k.
//
// The inputs are canonicalized so the longer side indexes rows; because
// the band is symmetric under transposition, Compare(a,b,k) and
// Compare(b,a,k) always report identical Distance and Stats.Cells.
func (m Matcher) Compare(a, b string, k int) (Result, error) {
	if k < 0 {
		return Result{}, ErrNegativeK
	}
	ra, err := decodeRunes(a, 'a')
	if err != nil {
		return Result{}, err
	}
	rb, err := decodeRunes(b, 'b')
	if err != nil {
		return Result{}, err
	}
	if m.FoldCase {
		foldRunes(ra)
		foldRunes(rb)
	}
	if len(ra) < len(rb) {
		ra, rb = rb, ra
	}
	n, width := len(ra), len(rb)

	// The distance is at least the length difference; if that alone
	// exceeds k we answer without filling a single DP cell.
	if n-width > k {
		return Result{Exceeded: true}, nil
	}
	if width == 0 {
		// Both empty (n == 0) or one side empty with n <= k.
		return Result{Distance: n}, nil
	}

	prev := make([]int, width+1)
	curr := make([]int, width+1)
	stats := Stats{MaxWorkLen: 2 * (width + 1)}
	inf := k + 1 // sentinel: every value > k is interchangeable

	for j := 0; j <= width; j++ {
		if j <= k {
			prev[j] = j
		} else {
			prev[j] = inf
		}
	}

	for i := 1; i <= n; i++ {
		lo := i - k
		if lo < 1 {
			lo = 1
		}
		hi := i + k
		if hi > width {
			hi = width
		}
		if i <= k {
			curr[0] = i
		} else {
			curr[0] = inf
		}
		if lo > 1 {
			curr[lo-1] = inf
		}
		rowMin := inf
		ca := ra[i-1]
		for j := lo; j <= hi; j++ {
			cost := 0
			if ca != rb[j-1] {
				cost = 1
			}
			v := min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
			curr[j] = v
			stats.Cells++
			if v < rowMin {
				rowMin = v
			}
		}
		if hi < width {
			curr[hi+1] = inf
		}
		// Every monotone path to (n, width) crosses row i inside the
		// band (crossing outside costs > k by length difference), so a
		// row minimum above k proves the distance exceeds k.
		if rowMin > k {
			return Result{Exceeded: true, Stats: stats}, nil
		}
		prev, curr = curr, prev
	}

	if d := prev[width]; d <= k {
		return Result{Distance: d, Stats: stats}, nil
	}
	return Result{Exceeded: true, Stats: stats}, nil
}
