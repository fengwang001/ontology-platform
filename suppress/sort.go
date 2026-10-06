package suppress

func radixSortDirectives(directives []acceptedDirective) {
	if len(directives) < 2 {
		return
	}
	buffer := make([]acceptedDirective, len(directives))
	for shift := uint(0); shift < 64; shift += 8 {
		buckets := make([][]acceptedDirective, 256)
		for _, directive := range directives {
			digit := byte(uint64(directive.line) >> shift)
			buckets[digit] = append(buckets[digit], directive)
		}
		ordered := buffer[:0]
		for _, bucket := range buckets {
			ordered = append(ordered, bucket...)
		}
		copy(directives, ordered)
	}
}

func radixSortDiagnosticsByLine(entries []diagnosticEntry) {
	if len(entries) < 2 {
		return
	}

	buffer := make([]diagnosticEntry, len(entries))
	sortDiagnosticsByUint64(entries, buffer, func(entry diagnosticEntry) uint64 {
		return uint64(entry.diagnostic.Line)
	})
}

func radixSortDiagnosticsForOutput(entries []diagnosticEntry) {
	if len(entries) < 2 {
		return
	}

	buffer := make([]diagnosticEntry, len(entries))
	maxRuleLength := 0
	for _, entry := range entries {
		if len(entry.diagnostic.Rule) > maxRuleLength {
			maxRuleLength = len(entry.diagnostic.Rule)
		}
	}

	from := entries
	to := buffer
	for index := maxRuleLength - 1; index >= 0; index-- {
		buckets := make([][]diagnosticEntry, 257)
		for _, entry := range from {
			digit := byte(0)
			if index < len(entry.diagnostic.Rule) {
				digit = entry.diagnostic.Rule[index] + 1
			}
			buckets[digit] = append(buckets[digit], entry)
		}
		ordered := to[:0]
		for _, bucket := range buckets {
			ordered = append(ordered, bucket...)
		}
		from, to = ordered, from
	}
	sortDiagnosticsByUint64(from, to, func(entry diagnosticEntry) uint64 {
		return uint64(entry.diagnostic.Column)
	})
	sortDiagnosticsByUint64(from, to, func(entry diagnosticEntry) uint64 {
		return uint64(entry.diagnostic.Line)
	})
	if &from[0] != &entries[0] {
		copy(entries, from)
	}
}

func sortDiagnosticsByUint64(entries, buffer []diagnosticEntry, value func(diagnosticEntry) uint64) {
	for shift := uint(0); shift < 64; shift += 8 {
		buckets := make([][]diagnosticEntry, 256)
		for _, entry := range entries {
			digit := byte(value(entry) >> shift)
			buckets[digit] = append(buckets[digit], entry)
		}
		ordered := buffer[:0]
		for _, bucket := range buckets {
			ordered = append(ordered, bucket...)
		}
		copy(entries, ordered)
	}
}

func issuePriority(category IssueCategory) int {
	switch category {
	case IssueMissingReason:
		return 1
	case IssueNoTargetLine:
		return 2
	case IssueUnknownRule:
		return 3
	case IssueRedundantDisable:
		return 4
	case IssueOrphanEnable:
		return 5
	case IssueUnused:
		return 6
	case IssueUnclosedRange:
		return 7
	default:
		return 8
	}
}
