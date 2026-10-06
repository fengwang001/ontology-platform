package suppress

func (state *analysisState) report() *Report {
	radixSortDiagnosticsForOutput(state.diagnostics)
	state.sortIssues()
	result := &Report{Issues: state.issues}
	for _, entry := range state.diagnostics {
		if entry.suppressor == nil {
			result.Kept = append(result.Kept, entry.diagnostic)
			continue
		}
		result.Suppressed = append(result.Suppressed, SuppressedDiagnostic{
			Diagnostic:    entry.diagnostic,
			DirectiveLine: entry.suppressor.directiveLine,
			Tag:           entry.suppressor.tag,
		})
	}
	return result
}

func (state *analysisState) sortIssues() {
	if len(state.issues) < 2 {
		return
	}

	from := state.issues
	to := make([]DirectiveIssue, len(from))
	countingIssues(from, to, 9, func(issue DirectiveIssue) int {
		return issuePriority(issue.Category) - 1
	})
	from, to = to, from

	countingIssues(from, to, 2, func(issue DirectiveIssue) int {
		if issue.InstructionLevel {
			return 0
		}
		return 1
	})
	from, to = to, from

	maxTagLength := 0
	for _, issue := range from {
		if len(issue.Tag) > maxTagLength {
			maxTagLength = len(issue.Tag)
		}
	}
	for index := maxTagLength - 1; index >= 0; index-- {
		countingIssues(from, to, 257, func(issue DirectiveIssue) int {
			if index >= len(issue.Tag) {
				return 0
			}
			return int(issue.Tag[index]) + 1
		})
		from, to = to, from
	}

	for shift := uint(0); shift < 64; shift += 8 {
		countingIssues(from, to, 256, func(issue DirectiveIssue) int {
			return int(byte(uint64(issue.DirectiveLine) >> shift))
		})
		from, to = to, from
	}

	copy(state.issues, from)
}

func countingIssues(from, to []DirectiveIssue, bucketCount int, bucket func(DirectiveIssue) int) {
	counts := make([]int, bucketCount)
	for _, issue := range from {
		counts[bucket(issue)]++
	}
	offsets := make([]int, bucketCount)
	for i := 1; i < bucketCount; i++ {
		offsets[i] = offsets[i-1] + counts[i-1]
	}
	for _, issue := range from {
		index := bucket(issue)
		to[offsets[index]] = issue
		offsets[index]++
	}
}
