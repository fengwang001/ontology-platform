// Package ontology implements a thresholded edit-distance matcher.
//
// Given two strings and an upper bound k, Distance reports whether their
// Levenshtein distance is at most k. If it is, the exact distance is
// returned; if it is not, the computation terminates early and reports
// "exceeded k" without ever computing the full distance.
//
// Algorithm: a banded dynamic program over runes. Only the diagonals
// |i-j| <= k of the DP matrix can contain values <= k, so only a band of
// width at most 2k+1 cells per row is evaluated. Two rolling arrays of
// that width give O(min(2k+1, min(lenA,lenB)+1)) memory. If every cell of
// a band row exceeds k, no later row can come back under k, so the search
// stops immediately. A length-difference check (|lenA-lenB| > k) rejects
// before any cell is filled.
//
// Semantics: comparison units are Unicode code points (runes), not bytes.
// Invalid UTF-8 is rejected with a *UTF8Error naming the side and byte
// offset; nothing is silently replaced with RuneError.
//
// Case folding: Options.FoldCase compares runes up to Unicode simple case
// folding (unicode.SimpleFold orbits). This is a 1:1 rune mapping, so
// full-fold expansions such as "Straße" vs "STRASSE" (ß -> ss) are NOT
// equated; "straße" vs "STRASSE" is, because ß simple-folds to ẞ/ß only.
//
// Symmetry: Distance(a,b) and Distance(b,a) return identical distances
// and identical Stats.CellsFilled. The band |i-j| <= k is symmetric under
// transposition and both sides stop on the same transposed row. Memory is
// kept O(min(len)) by always laying the shorter rune sequence along the
// rolling-array axis; this swap does not change the cell count.
package ontology
