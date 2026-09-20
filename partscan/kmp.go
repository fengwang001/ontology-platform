package partscan

// kmp is an incremental Knuth-Morris-Pratt matcher for a fixed byte pattern.
// State is the length of the longest pattern prefix that matches a suffix of
// the bytes fed so far. Feeding one byte advances the state in amortized O(1).
type kmp struct {
	pattern []byte
	next    []int // failure function: next[i] for matched length i
	state   int
}

func newKMP(pattern []byte) *kmp {
	m := &kmp{pattern: pattern, next: make([]int, len(pattern)+1)}
	m.next[0] = -1
	m.next[1] = 0
	for i := 2; i <= len(pattern); i++ {
		j := m.next[i-1]
		for j >= 0 && pattern[i-1] != pattern[j] {
			j = m.next[j]
		}
		m.next[i] = j + 1
	}
	return m
}

func (m *kmp) reset() { m.state = 0 }

// push feeds one byte and reports whether the pattern is now fully matched.
func (m *kmp) push(b byte) bool {
	if m.state == len(m.pattern) {
		m.state = m.next[m.state]
	}
	for m.state >= 0 && b != m.pattern[m.state] {
		m.state = m.next[m.state]
	}
	m.state++
	return m.state == len(m.pattern)
}

// failState handles a look-ahead byte b that disproves a tentative full match.
// The full pattern is first treated as a failed extension (the longest proper
// prefix that is also a suffix), then b is pushed against that fallback.
func (m *kmp) failState(b byte) bool {
	m.state = m.next[m.state]
	return m.push(b)
}
