// Package scan runs the single-pass matcher: it walks the text with
// an index that never moves backward and reports every start
// position of the pattern, overlaps included.
package scan

import (
	"sync/atomic"

	"ontology/table"
)

// Matcher scans texts using one compiled failure table. It is safe
// for concurrent use; the internal counters are shared across
// goroutines and are only exact for a single-goroutine scan.
type Matcher struct {
	tab table.Table

	textAdvances atomic.Int64 // times the text index moved forward
	comparisons  atomic.Int64 // byte comparisons performed
}

// New returns a Matcher bound to tab.
func New(tab table.Table) *Matcher { return &Matcher{tab: tab} }

// Scan returns every start position of the pattern in text.
// The text index j only ever increases.
func (m *Matcher) Scan(text string) []int {
	width := m.tab.Len()
	hits := make([]int, 0, 8)
	if width == 0 || len(text) < width {
		return hits
	}
	k := 0 // matched length; falls back to tab.At(k) on mismatch
	for j := 0; j < len(text); {
		m.comparisons.Add(1)
		if text[j] == m.tab.Byte(k) {
			k++
			j++
			m.textAdvances.Add(1)
			if k == width {
				hits = append(hits, j-width)
				k = m.tab.At(k)
			}
		} else if k > 0 {
			k = m.tab.At(k)
		} else {
			j++
			m.textAdvances.Add(1)
		}
	}
	return hits
}

// Stats reports the internal counters: text-index advances and byte
// comparisons since the Matcher was created.
func (m *Matcher) Stats() (textAdvances, comparisons int64) {
	return m.textAdvances.Load(), m.comparisons.Load()
}
