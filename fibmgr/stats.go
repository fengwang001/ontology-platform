package fibmgr

// Internal work counters, exported only so tests and benchmarks can
// verify that update cost does not grow with the total number of
// unrelated routes. Updated while the manager lock is held; not safe
// to read concurrently with updates.
var stats struct {
	// Nodes counts trie nodes touched by reaggregation/entry fixup.
	Nodes int64
	// ColorOps counts candidate-set elements scanned while combining.
	ColorOps int64
}

// ResetStats zeroes the internal work counters.
func ResetStats() {
	stats.Nodes = 0
	stats.ColorOps = 0
}

// Stats returns the internal work counters: trie nodes touched and
// candidate-set elements scanned since the last ResetStats.
func Stats() (nodes, colorOps int64) {
	return stats.Nodes, stats.ColorOps
}
