package snapshot

import "encoding/json"

// RecoveryResult is the outcome of recovering one snapshot. It is a pure
// function of the input bytes: recovering the same snapshot any number of
// times, from any number of concurrent callers, always yields an identical
// result.
type RecoveryResult struct {
	// Prefixes holds, per section, the length of the maximal recoverable
	// prefix (number of leading records that are individually intact).
	// Sections absent from the file have prefix 0.
	Prefixes map[SectionType]int
	// Objects/Links/Actions are the final retained records after
	// cross-section checks, in original file order.
	Objects []ObjectRecord
	Links   []LinkRecord
	Actions []ActionRecord
	// Diagnostics lists every anomaly found, ordered by category priority,
	// then canonical section order, then record index.
	Diagnostics []Diagnostic
	// Stats holds per-section work counters backing the complexity claim.
	Stats map[SectionType]SectionStats
}

// Recoverer performs snapshot recovery. It carries no mutable state, so a
// single instance is safe for concurrent use and all concurrent recoveries
// of the same input produce identical results.
type Recoverer struct{}

// NewRecoverer returns a ready-to-use Recoverer.
func NewRecoverer() *Recoverer { return &Recoverer{} }

// Recover locates each section's maximal recoverable prefix and then
// reconciles the prefixes across sections.
//
// The pipeline applies the four anomaly categories in their fixed priority
// order, each stage consuming the previous stage's output:
//
//  1. structural: scan each section independently for its intact prefix
//  2. count: compare declared vs. actual counts on cleanly scanned sections
//  3. cross-reference: drop links whose objects fell outside the prefix
//  4. action: drop action records whose dependencies were not retained
func (Recoverer) Recover(data []byte) *RecoveryResult {
	res := &RecoveryResult{
		Prefixes: make(map[SectionType]int, len(sectionOrder)),
		Stats:    make(map[SectionType]SectionStats, len(sectionOrder)),
	}
	for _, t := range sectionOrder {
		res.Prefixes[t] = 0
		res.Stats[t] = SectionStats{}
	}

	dir, err := parseDirectory(data)
	if err != nil {
		// File-level structural failure: section boundaries cannot be
		// trusted, so no prefix of any section is recoverable.
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Category:    CatStructural,
			Section:     0,
			RecordIndex: -1,
			Detail:      err.Error(),
		})
		return res
	}

	// Stage 1: per-section structural scan (independent per section).
	scans := make(map[SectionType]sectionScan, len(sectionOrder))
	for _, t := range sectionOrder {
		e, ok := dir[t]
		if !ok {
			continue // section absent: prefix stays 0, not an anomaly
		}
		scan := scanSection(data, e, validatorFor(t))
		scans[t] = scan
		res.Stats[t] = scan.stats
		res.Prefixes[t] = len(scan.payloads)
		if scan.corruption != nil {
			res.Diagnostics = append(res.Diagnostics, *scan.corruption)
		}
	}

	// Stage 2: declared-count check, only where stage 1 found no
	// structural corruption (fixed priority: structural wins the section).
	for _, t := range sectionOrder {
		scan, ok := scans[t]
		if !ok || scan.corruption != nil {
			continue
		}
		if d := checkDeclaredCount(t, scan.declared, len(scan.payloads)); d != nil {
			res.Diagnostics = append(res.Diagnostics, *d)
		}
	}

	objects := decodePrefix[ObjectRecord](scans[SectionObjects].payloads)
	links := decodePrefix[LinkRecord](scans[SectionLinks].payloads)
	actions := decodePrefix[ActionRecord](scans[SectionActions].payloads)

	objectIDs := make(map[string]struct{}, len(objects))
	for _, o := range objects {
		objectIDs[o.ObjectID] = struct{}{}
	}

	// Stage 3: cross-section reference check on links.
	keptLinks, linkDiags := filterLinks(links, objectIDs)
	res.Diagnostics = append(res.Diagnostics, linkDiags...)

	linkIDs := make(map[string]struct{}, len(keptLinks))
	for _, l := range keptLinks {
		linkIDs[l.LinkID] = struct{}{}
	}

	// Stage 4: action usability check against the final retained sets.
	// The action section's own prefix was fully determined in stage 1;
	// records are only dropped here, one by one, as their dependencies
	// turn out to be missing.
	keptActions, actionDiags := filterActions(actions, objectIDs, linkIDs)
	res.Diagnostics = append(res.Diagnostics, actionDiags...)

	res.Objects = objects
	res.Links = keptLinks
	res.Actions = keptActions
	return res
}

// decodePrefix unmarshals the recoverable prefix payloads of one section.
// Every payload here has already passed validation during the scan, so a
// decode error is impossible in practice; failures are skipped defensively.
func decodePrefix[T any](payloads [][]byte) []T {
	out := make([]T, 0, len(payloads))
	for _, p := range payloads {
		var v T
		if err := json.Unmarshal(p, &v); err == nil {
			out = append(out, v)
		}
	}
	return out
}
