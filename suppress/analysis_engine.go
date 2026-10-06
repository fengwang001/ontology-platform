package suppress

func (state *analysisState) run() {
	for _, directive := range state.directives {
		if directive.kind == KindFile {
			state.processDirective(directive)
		}
	}

	directiveIndex := 0
	diagnosticIndex := 0

	for directiveIndex < len(state.directives) || diagnosticIndex < len(state.diagnostics) {
		line := state.nextLine(directiveIndex, diagnosticIndex)
		for directiveIndex < len(state.directives) && state.directives[directiveIndex].line == line {
			directive := state.directives[directiveIndex]
			if directive.kind != KindFile {
				state.processDirective(directive)
			}
			directiveIndex++
		}
		for diagnosticIndex < len(state.diagnostics) && state.diagnostics[diagnosticIndex].diagnostic.Line == line {
			entry := &state.diagnostics[diagnosticIndex]
			key := suppressorKey{line: line, rule: entry.diagnostic.Rule}
			state.considerInterval(key, state.activeCandidate(key.rule))
			state.considerInterval(key, state.activeCandidate(AllRules))
			diagnosticIndex++
		}
	}

	for _, active := range state.active {
		candidate := active.opener
		state.candidates[candidate.identity()] = candidate
		if _, reported := state.unclosed[candidate.directiveLine]; !reported {
			state.unclosed[candidate.directiveLine] = struct{}{}
			state.addIssue(candidate.sequence, candidate.directiveLine, "", IssueUnclosedRange, true)
		}
	}
	state.assignSuppression()
	state.collectUnused()
}

func (state *analysisState) nextLine(directiveIndex, diagnosticIndex int) int {
	switch {
	case directiveIndex >= len(state.directives):
		return state.diagnostics[diagnosticIndex].diagnostic.Line
	case diagnosticIndex >= len(state.diagnostics):
		return state.directives[directiveIndex].line
	case state.directives[directiveIndex].line <= state.diagnostics[diagnosticIndex].diagnostic.Line:
		return state.directives[directiveIndex].line
	default:
		return state.diagnostics[diagnosticIndex].diagnostic.Line
	}
}

func (state *analysisState) processDirective(directive acceptedDirective) {
	missingReason := state.snap.requireReason && isBlank(directive.reason)
	for _, tag := range directive.tags {
		switch {
		case missingReason:
			state.addIssue(directive.sequence, directive.line, tag, IssueMissingReason, false)
		case directive.kind == KindNextLine && directive.line == state.snap.totalLines:
			state.addIssue(directive.sequence, directive.line, tag, IssueNoTargetLine, false)
		case tag != AllRules:
			if _, known := state.snap.knownRules[tag]; !known {
				state.addIssue(directive.sequence, directive.line, tag, IssueUnknownRule, false)
				continue
			}
			state.processValidTag(directive, tag, tagIndex(directive, tag))
		default:
			state.processValidTag(directive, tag, tagIndex(directive, tag))
		}
	}
}

func (state *analysisState) processValidTag(directive acceptedDirective, tag string, tagIndex int) {
	switch directive.kind {
	case KindEnable:
		if state.active[tag] == nil {
			state.addIssue(directive.sequence, directive.line, tag, IssueOrphanEnable, false)
			return
		}
		delete(state.active, tag)
	case KindDisable:
		if state.active[tag] != nil {
			state.addIssue(directive.sequence, directive.line, tag, IssueRedundantDisable, false)
			return
		}
		state.active[tag] = &activeRange{opener: state.newCandidate(directive, tag, tagIndex)}
	default:
		if state.snap.requireReason && isBlank(directive.reason) {
			return
		}
		if directive.kind == KindNextLine && directive.line == state.snap.totalLines {
			return
		}
		candidate := state.newCandidate(directive, tag, tagIndex)
		switch directive.kind {
		case KindLine:
			state.addPoint(candidate, directive.line)
		case KindNextLine:
			state.addPoint(candidate, directive.line+1)
		case KindFile:
			if existing := state.files[tag]; betterCandidate(candidate, existing) {
				state.files[tag] = candidate
			}
		}
	}
}

func (state *analysisState) newCandidate(directive acceptedDirective, tag string, tagIndex int) *suppressor {
	candidate := &suppressor{
		directiveLine: directive.line,
		sequence:      directive.sequence,
		tag:           tag,
		tagSpecific:   tag != AllRules,
		kind:          directive.kind,
		reason:        directive.reason,
	}
	candidate.id = directive.tagIDs[tagIndex]
	if _, exists := state.candidates[candidate.identity()]; !exists {
		state.candidates[candidate.identity()] = candidate
	}
	return candidate
}

func tagIndex(directive acceptedDirective, tag string) int {
	for index, candidateTag := range directive.tags {
		if candidateTag == tag {
			return index
		}
	}
	return len(directive.tags)
}

func (state *analysisState) addPoint(candidate *suppressor, line int) {
	key := suppressorKey{line: line, rule: candidate.tag}
	if existing := state.points[key]; betterCandidate(candidate, existing) {
		state.points[key] = candidate
	}
}

func (state *analysisState) considerInterval(key suppressorKey, candidate *suppressor) {
	if candidate == nil {
		return
	}
	if existing := state.allCandidates[key]; betterCandidate(candidate, existing) {
		state.allCandidates[key] = candidate
	}
}

func (state *analysisState) considerActive(key suppressorKey, tag string) {
	if active := state.active[tag]; active != nil {
		state.considerInterval(key, active.opener)
	}
}

func (state *analysisState) considerPoint(key suppressorKey, tag string) {
	candidate := state.points[suppressorKey{line: key.line, rule: tag}]
	if candidate == nil {
		return
	}
	if existing := state.allCandidates[key]; betterCandidate(candidate, existing) {
		state.allCandidates[key] = candidate
	}
}

func (state *analysisState) assignSuppression() {
	for key, entries := range state.diagnosticAt {
		candidates := []*suppressor{state.allCandidates[key]}
		candidates = append(candidates, state.points[key])
		candidates = append(candidates, state.points[suppressorKey{line: key.line, rule: AllRules}])
		candidates = append(candidates, state.files[key.rule])
		candidates = append(candidates, state.files[AllRules])

		winner := bestCandidate(candidates)
		if winner == nil {
			continue
		}
		winner.used = true
		for _, entry := range entries {
			entry.suppressor = winner
		}
	}
}

func (state *analysisState) activeCandidate(tag string) *suppressor {
	if active := state.active[tag]; active != nil {
		return active.opener
	}
	return nil
}

func (state *analysisState) collectUnused() {
	seen := make(map[suppressorIdentity]struct{})
	mark := func(candidate *suppressor) {
		if candidate == nil || candidate.kind == KindEnable {
			return
		}
		identity := candidate.identity()
		if _, exists := seen[identity]; exists {
			return
		}
		seen[identity] = struct{}{}
		if !candidate.used {
			state.addIssue(candidate.sequence, candidate.directiveLine, candidate.tag, IssueUnused, false)
		}
	}
	for _, candidate := range state.candidates {
		mark(candidate)
	}
}

func (state *analysisState) addIssue(sequence int, line int, tag string, category IssueCategory, instructionLevel bool) {
	key := reportedIssueKey{sequence: sequence, tag: tag, category: category}
	if _, exists := state.reportedIssues[key]; exists {
		return
	}
	state.reportedIssues[key] = struct{}{}
	state.issues = append(state.issues, DirectiveIssue{
		DirectiveLine:    line,
		Tag:              tag,
		Category:         category,
		InstructionLevel: instructionLevel,
	})
}

func bestCandidate(candidates []*suppressor) *suppressor {
	var best *suppressor
	for _, candidate := range candidates {
		if candidate != nil && (best == nil || betterCandidate(candidate, best)) {
			best = candidate
		}
	}
	return best
}

func betterCandidate(candidate, existing *suppressor) bool {
	if existing == nil {
		return true
	}
	if candidate.directiveLine != existing.directiveLine {
		return candidate.directiveLine < existing.directiveLine
	}
	if candidate.sequence != existing.sequence {
		return candidate.sequence < existing.sequence
	}
	return candidate.tagSpecific && !existing.tagSpecific
}

func isBlank(value string) bool {
	for _, r := range value {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}
