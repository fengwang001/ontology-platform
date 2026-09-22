package canon

import (
	"strings"
	"testing"
)

// buildURL makes a URL of roughly targetLen bytes, mixing percent
// escapes, dot segments and query items so all scanners are exercised.
func buildURL(targetLen int) string {
	var b strings.Builder
	b.WriteString("http://example.com")
	seg := "/a%2Fb/./%41"
	for b.Len() < targetLen*3/4 {
		b.WriteString(seg)
	}
	b.WriteString("/leaf?x=%7E&y=1")
	for b.Len() < targetLen {
		b.WriteString("&k=v%41")
	}
	return b.String()
}

// TestScanCountLinear proves the byte-scan counter grows linearly with
// input length: 64x the input must cost ~64x the scans, not 64^2.
func TestScanCountLinear(t *testing.T) {
	n := New(ModeSorted, Limits{MaxURLLength: 1 << 20, MaxPathSegments: 1 << 20, MaxQueryParams: 1 << 20})
	u1 := buildURL(1 << 10)   // ~1 KB
	u64 := buildURL(64 << 10) // ~64 KB

	n.scans.Store(0)
	if _, err := n.Normalize(u1); err != nil {
		t.Fatal(err)
	}
	s1 := n.ScanCount()

	n.scans.Store(0)
	if _, err := n.Normalize(u64); err != nil {
		t.Fatal(err)
	}
	s64 := n.ScanCount()

	ratio := float64(s64) / float64(s1)
	lenRatio := float64(len(u64)) / float64(len(u1))
	t.Logf("L1=%d scans=%d; L64=%d scans=%d; scan ratio=%.1f, length ratio=%.1f",
		len(u1), s1, len(u64), s64, ratio, lenRatio)
	if ratio < lenRatio*0.5 || ratio > lenRatio*1.5 {
		t.Fatalf("scan count not linear: ratio %.1f vs length ratio %.1f", ratio, lenRatio)
	}
	// Absolute sanity: never more than a small constant factor per byte.
	if s64 > uint64(8*len(u64)) {
		t.Fatalf("too many scans: %d for %d bytes", s64, len(u64))
	}
}
