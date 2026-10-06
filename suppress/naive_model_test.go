package suppress

import (
	"fmt"
	"sort"
	"strings"
)

type naiveCandidate struct {
	directive acceptedDirective
	tag       string
	specific  bool
}

func naiveDecide(snap snapshot) *Report {
	directives := append([]acceptedDirective(nil), snap.directives...)
	sort.SliceStable(directives, func(i, j int) bool {
		if directives[i].line != directives[j].line {
			return directives[i].line < directives[j].line
		}
		return directives[i].sequence < directives[j].sequence
	})
	diagnostics := make([]diagnosticEntry, len(snap.diagnostics))
	for i := range snap.diagnostics {
		diagnostics[i] = diagnosticEntry{diagnostic: snap.diagnostics[i], sequence: i}
	}
	radixSortDiagnosticsForOutput(diagnostics)

	candidateKey := func(directive acceptedDirective, tag string) string {
		return fmt.Sprintf("%d\x00%s\x00%s", directive.sequence, directive.kind, tag)
	}
	candidateCache := map[string]*naiveCandidate{}
	makeCandidate := func(directive acceptedDirective, tag string) *naiveCandidate {
		key := candidateKey(directive, tag)
		if candidate := candidateCache[key]; candidate != nil {
			return candidate
		}
		candidate := &naiveCandidate{directive: directive, tag: tag, specific: tag != AllRules}
		candidateCache[key] = candidate
		return candidate
	}
	better := func(candidate, existing *naiveCandidate) bool {
		if existing == nil {
			return true
		}
		left, right := candidate.directive, existing.directive
		if left.line != right.line {
			return left.line < right.line
		}
		if left.sequence != right.sequence {
			return left.sequence < right.sequence
		}
		return candidate.specific && !existing.specific
	}

	var issues []DirectiveIssue
	issueSet := map[string]struct{}{}
	addIssue := func(directive acceptedDirective, line int, tag string, category IssueCategory, instructionLevel bool) {
		key := fmt.Sprintf("%d\x00%s\x00%t\x00%s\x00%s", directive.sequence, directive.kind, instructionLevel, tag, category)
		if _, exists := issueSet[key]; exists {
			return
		}
		issueSet[key] = struct{}{}
		issues = append(issues, DirectiveIssue{DirectiveLine: line, Tag: tag, Category: category, InstructionLevel: instructionLevel})
	}

	active := map[string]*naiveCandidate{}
	points := map[suppressorKey]*naiveCandidate{}
	files := map[string]*naiveCandidate{}
	candidateSet := map[string]*naiveCandidate{}
	usedSet := map[string]struct{}{}
	diagnosticsAt := map[suppressorKey][]diagnosticEntry{}
	for _, entry := range diagnostics {
		key := suppressorKey{line: entry.diagnostic.Line, rule: entry.diagnostic.Rule}
		diagnosticsAt[key] = append(diagnosticsAt[key], entry)
	}

	// 全文件指令不依赖所在行，先独立登记。
	for _, directive := range directives {
		if directive.kind != KindFile {
			continue
		}
		for _, tag := range directive.tags {
			if snap.requireReason && strings.TrimSpace(directive.reason) == "" {
				continue
			}
			if tag != AllRules {
				if _, known := snap.knownRules[tag]; !known {
					continue
				}
			}
			candidate := makeCandidate(directive, tag)
			candidateSet[candidateKey(directive, tag)] = candidate
			if existing := files[tag]; better(candidate, existing) {
				files[tag] = candidate
			}
		}
	}

	var kept []Diagnostic
	var suppressed []SuppressedDiagnostic
	directiveIndex := 0
	for line := 1; line <= snap.totalLines; line++ {
		linePoints := map[suppressorKey]*naiveCandidate{}
		for directiveIndex < len(directives) && directives[directiveIndex].line == line {
			directive := directives[directiveIndex]
			directiveIndex++
			for _, tag := range directive.tags {
				if snap.requireReason && strings.TrimSpace(directive.reason) == "" {
					addIssue(directive, line, tag, IssueMissingReason, false)
					continue
				}
				if directive.kind == KindNextLine && line == snap.totalLines {
					addIssue(directive, line, tag, IssueNoTargetLine, false)
					continue
				}
				if tag != AllRules {
					if _, known := snap.knownRules[tag]; !known {
						addIssue(directive, line, tag, IssueUnknownRule, false)
						continue
					}
				}

				candidate := makeCandidate(directive, tag)
				switch directive.kind {
				case KindLine:
					key := suppressorKey{line: line, rule: tag}
					linePoints[key] = candidate
					candidateSet[candidateKey(directive, tag)] = candidate
				case KindNextLine:
					key := suppressorKey{line: line + 1, rule: tag}
					if existing := points[key]; better(candidate, existing) {
						points[key] = candidate
					}
					candidateSet[candidateKey(directive, tag)] = candidate
				case KindDisable:
					if active[tag] != nil {
						addIssue(directive, line, tag, IssueRedundantDisable, false)
					} else {
						active[tag] = candidate
						candidateSet[candidateKey(directive, tag)] = candidate
					}
				case KindEnable:
					if active[tag] == nil {
						addIssue(directive, line, tag, IssueOrphanEnable, false)
					} else {
						candidateSet[candidateKey(active[tag].directive, active[tag].tag)] = active[tag]
						delete(active, tag)
					}
				}
			}
		}
		for key, candidate := range linePoints {
			if existing := points[key]; better(candidate, existing) {
				points[key] = candidate
			}
		}

		for _, entry := range diagnostics {
			if entry.diagnostic.Line != line {
				continue
			}
			key := suppressorKey{line: line, rule: entry.diagnostic.Rule}
			candidates := []*naiveCandidate{
				active[key.rule], active[AllRules],
				points[key], points[suppressorKey{line: line, rule: AllRules}],
				files[key.rule], files[AllRules],
			}
			var winner *naiveCandidate
			for _, candidate := range candidates {
				if candidate != nil && better(candidate, winner) {
					winner = candidate
				}
			}
			if winner == nil {
				kept = append(kept, entry.diagnostic)
				continue
			}
			usedSet[candidateKey(winner.directive, winner.tag)] = struct{}{}
			suppressed = append(suppressed, SuppressedDiagnostic{Diagnostic: entry.diagnostic, DirectiveLine: winner.directive.line, Tag: winner.tag})
		}
	}

	unclosed := map[int]acceptedDirective{}
	for tag, candidate := range active {
		unclosed[candidate.directive.line] = candidate.directive
		candidateSet[candidateKey(candidate.directive, tag)] = candidate
	}
	for line, directive := range unclosed {
		addIssue(directive, line, "", IssueUnclosedRange, true)
	}
	for key, candidate := range candidateSet {
		if candidate.directive.kind != KindEnable {
			if _, used := usedSet[key]; !used {
				addIssue(candidate.directive, candidate.directive.line, candidate.tag, IssueUnused, false)
			}
		}
	}
	sort.SliceStable(issues, func(i, j int) bool {
		left, right := issues[i], issues[j]
		if left.DirectiveLine != right.DirectiveLine {
			return left.DirectiveLine < right.DirectiveLine
		}
		if left.InstructionLevel != right.InstructionLevel {
			return left.InstructionLevel
		}
		if left.Tag != right.Tag {
			return left.Tag < right.Tag
		}
		return issuePriority(left.Category) < issuePriority(right.Category)
	})
	return &Report{Kept: kept, Suppressed: suppressed, Issues: issues}
}
