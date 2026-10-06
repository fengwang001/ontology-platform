package suppress

import (
	"fmt"
	"strings"
	"sync"
)

type Processor struct {
	mu            sync.RWMutex
	totalLines    int
	requireReason bool
	knownRules    map[string]struct{}
	diagnostics   []Diagnostic
	directives    []acceptedDirective
	nextSequence  int
	directiveKeys map[string]struct{}
}

func NewProcessor(totalLines int, knownRules []string, requireReason bool) *Processor {
	ruleSet := make(map[string]struct{}, len(knownRules))
	for _, rule := range knownRules {
		ruleSet[rule] = struct{}{}
	}
	return &Processor{
		totalLines:    totalLines,
		requireReason: requireReason,
		knownRules:    ruleSet,
		directiveKeys: make(map[string]struct{}),
	}
}

func (p *Processor) RegisterDiagnostic(diagnostic Diagnostic) error {
	if diagnostic.Line < 1 || diagnostic.Line > p.totalLines {
		return fmt.Errorf("%w: diagnostic line %d is outside 1..%d", ErrInvalidArgument, diagnostic.Line, p.totalLines)
	}
	if diagnostic.Column < 1 {
		return fmt.Errorf("%w: diagnostic column %d is less than 1", ErrInvalidArgument, diagnostic.Column)
	}
	if diagnostic.Rule == "" {
		return fmt.Errorf("%w: diagnostic rule is empty", ErrInvalidArgument)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.diagnostics = append(p.diagnostics, diagnostic)
	return nil
}

func (p *Processor) RegisterDirective(directive Directive) error {
	if directive.Line < 1 || directive.Line > p.totalLines {
		return fmt.Errorf("%w: directive line %d is outside 1..%d", ErrInvalidArgument, directive.Line, p.totalLines)
	}
	if !isKnownKind(directive.Kind) {
		return fmt.Errorf("%w: unknown directive kind %q", ErrInvalidArgument, directive.Kind)
	}

	tags := normalizeTags(directive.Tags)
	key := directiveKey(directive, tags)

	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.directiveKeys[key]; exists {
		return fmt.Errorf("%w: %s directive at line %d", ErrDuplicateDirective, directive.Kind, directive.Line)
	}

	p.directiveKeys[key] = struct{}{}
	p.directives = append(p.directives, acceptedDirective{
		line:     directive.Line,
		kind:     directive.Kind,
		tags:     tags,
		reason:   directive.Reason,
		sequence: p.nextSequence,
	})
	p.nextSequence++
	return nil
}

func (p *Processor) Decide() *Report {
	p.mu.RLock()
	snap := snapshot{
		totalLines:    p.totalLines,
		requireReason: p.requireReason,
		knownRules:    make(map[string]struct{}, len(p.knownRules)),
		diagnostics:   append([]Diagnostic(nil), p.diagnostics...),
		directives:    make([]acceptedDirective, len(p.directives)),
	}
	for rule := range p.knownRules {
		snap.knownRules[rule] = struct{}{}
	}
	copy(snap.directives, p.directives)
	p.mu.RUnlock()

	for i := range snap.directives {
		snap.directives[i].tags = append([]string(nil), snap.directives[i].tags...)
	}
	return analyze(snap)
}

func isKnownKind(kind DirectiveKind) bool {
	switch kind {
	case KindLine, KindNextLine, KindDisable, KindEnable, KindFile:
		return true
	default:
		return false
	}
}

func normalizeTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	normalized := make([]string, 0, len(tags)+1)
	for _, tag := range tags {
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		normalized = append(normalized, tag)
	}
	if len(normalized) == 0 {
		return []string{AllRules}
	}
	return normalized
}

func directiveKey(directive Directive, tags []string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "%d\x00%s\x00%s\x00", directive.Line, directive.Kind, directive.Reason)
	for _, tag := range tags {
		builder.WriteString(tag)
		builder.WriteByte(0)
	}
	return builder.String()
}
