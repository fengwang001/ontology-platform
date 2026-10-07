package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// BlockResult is the standalone verification result of one block. Block-level
// verification depends only on the block's own file; nothing about another
// block can change it.
type BlockResult struct {
	Type        string
	Status      string
	Declared    int
	Actual      int
	Records     []Record // non-nil when the payload authenticates and parses
	RefVerdicts []RefVerdict
}

// Trusted reports whether the block content itself is trustworthy: integrity
// verified AND declared/actual record counts agree.
func (b *BlockResult) Trusted() bool { return b.Status == StatusOK }

// RefVerdict is the independent verdict for one link occurrence. The two
// directions of a bidirectional cross-block link are separate link
// occurrences and receive separate verdicts; one direction resolving is
// never used as evidence for the other.
type RefVerdict struct {
	SourceType string
	SourceID   string
	Field      string
	TargetType string
	TargetID   string
	Verdict    string
}

// Report is the complete, deterministic verification result of an export. It
// is a pure function of the bytes on disk: repeated concurrent Verify calls
// against the same untouched directory return equal reports.
type Report struct {
	Types   []string
	Blocks  map[string]*BlockResult
	indexes map[string]*idIndex // lazily built, not exposed
}

// Loader performs read-only verification of an on-disk export. A Loader owns
// no mutable per-export state and is safe for concurrent use by many
// goroutines against the same directory.
type Loader struct{}

func NewLoader() *Loader { return &Loader{} }

// Verify re-reads every chunk file from disk. It never writes, moves or
// deletes anything, hence it is idempotent and cannot alter prior verdicts.
func (l *Loader) Verify(ctx context.Context, dir string, logger *DecisionLogger) (*Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = NewDecisionLogger()
	}

	mb, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var man manifest
	if err := json.Unmarshal(mb, &man); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	types := append([]string(nil), man.Types...)
	sort.Strings(types)
	logger.Log("manifest", dir, joinTypes(types), "manifest.json lists export coverage")

	report := &Report{Types: types, Blocks: make(map[string]*BlockResult, len(types))}

	// Stage 1+2: per-block checksums and count checks, strictly ordered.
	for _, typ := range types {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		report.Blocks[typ] = verifyBlockFile(dir, typ, logger)
	}

	// Stage 3: cross-block reference verdicts. Only blocks that passed both
	// block-local stages get their references examined.
	for _, typ := range types {
		br := report.Blocks[typ]
		if br.Status != StatusOK {
			logger.Log("refs:skip", typ, "no verdicts",
				"block status "+br.Status+" prevents trustworthy reference claims")
			continue
		}
		for _, rec := range br.Records {
			for _, link := range rec.Links {
				br.RefVerdicts = append(br.RefVerdicts, judgeRef(typ, rec.ID, link, report, logger))
			}
		}
	}
	return report, nil
}

// verifyBlockFile runs block-local stages only. The result never depends on
// any other block.
func verifyBlockFile(dir, typ string, logger *DecisionLogger) *BlockResult {
	br := &BlockResult{Type: typ}
	path := chunkPath(dir, typ)
	raw, err := os.ReadFile(path)
	if err != nil {
		br.Status = StatusChecksumFailed
		logger.Log("checksum", typ, StatusChecksumFailed, "chunk file unreadable: "+err.Error())
		return br
	}
	var cf chunkFile
	if err := json.Unmarshal(raw, &cf); err != nil {
		br.Status = StatusChecksumFailed
		logger.Log("checksum", typ, StatusChecksumFailed, "chunk envelope unparseable: "+err.Error())
		return br
	}
	if cf.Type != typ {
		br.Status = StatusChecksumFailed
		logger.Log("checksum", typ, StatusChecksumFailed,
			fmt.Sprintf("envelope type %q does not match chunk name %q", cf.Type, typ))
		return br
	}
	br.Declared = cf.Count

	payload, derr := hex.DecodeString(cf.Payload)
	sum := sha256.Sum256(orEmpty(payload))
	actual := hex.EncodeToString(sum[:])
	// A hex decode failure implies authenticated bytes are not recoverable;
	// it is a block-integrity failure, never a count issue.
	if derr != nil || actual != cf.Checksum {
		br.Status = StatusChecksumFailed
		basis := "payload hex undecodable"
		if derr == nil {
			basis = fmt.Sprintf("sha256(payload)=%s != declared %s", actual, cf.Checksum)
		}
		logger.Log("checksum", typ, StatusChecksumFailed, basis)
		return br
	}
	logger.Log("checksum", typ, "pass", "sha256(payload)="+actual+" matches envelope")

	var records []Record
	if err := json.Unmarshal(payload, &records); err != nil {
		br.Status = StatusChecksumFailed
		logger.Log("checksum", typ, StatusChecksumFailed,
			"authenticated payload is not a record array: "+err.Error())
		return br
	}
	br.Records = records
	br.Actual = len(records)

	// Count stage: mismatch means truncation vs. inflation is undecidable, so
	// the ENTIRE block is untrusted. Individual records that look fine must
	// not be salvaged.
	if br.Actual != br.Declared {
		br.Status = StatusCountMismatch
		br.Records = nil
		logger.Log("count", typ, StatusCountMismatch,
			fmt.Sprintf("declared %d records, parsed %d: truncation/inflation undecidable",
				br.Declared, br.Actual))
		return br
	}
	br.Status = StatusOK
	logger.Log("count", typ, "pass", fmt.Sprintf("declared and parsed counts both %d", br.Actual))
	return br
}

func orEmpty(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}

// judgeRef resolves one link conservatively:
//   - target block missing from export coverage: no matching object can exist,
//     so the reference is dangling;
//   - target block present but untrusted: dangling-vs-valid is UNDECIDABLE,
//     reported as target_untrusted (never defaulted either way);
//   - target block trusted: O(1) hash-set membership decides.
func judgeRef(sourceType, sourceID string, link Link, report *Report, logger *DecisionLogger) RefVerdict {
	v := RefVerdict{
		SourceType: sourceType, SourceID: sourceID, Field: link.Field,
		TargetType: link.Target, TargetID: link.ID,
	}
	target, covered := report.Blocks[link.Target]
	switch {
	case !covered:
		v.Verdict = RefDangling
		logger.Log("ref", refInput(v), RefDangling,
			"target type "+link.Target+" is outside export coverage, no matching object can exist")
	case target.Status != StatusOK:
		v.Verdict = RefTargetUntrusted
		logger.Log("ref", refInput(v), RefTargetUntrusted,
			"target block "+link.Target+" status "+target.Status+": dangling vs. valid is undecidable")
	default:
		if targetIDIndex(report, link.Target).Has(link.ID) {
			v.Verdict = RefResolved
			logger.Log("ref", refInput(v), RefResolved, "id found in trusted target block hash index")
		} else {
			v.Verdict = RefDangling
			logger.Log("ref", refInput(v), RefDangling, "id absent from trusted target block hash index")
		}
	}
	return v
}

// idIndexes memoises one hash set per trusted block per verification run.
// Building costs O(N) once across all references; each individual lookup is
// expected O(1) and instrumented via idIndex.ProbeCount.
func targetIDIndex(report *Report, typ string) *idIndex {
	if report.indexes == nil {
		report.indexes = make(map[string]*idIndex)
	}
	idx, ok := report.indexes[typ]
	if !ok {
		br := report.Blocks[typ]
		idx = newIDIndex(len(br.Records))
		for _, rec := range br.Records {
			idx.Add(rec.ID)
		}
		report.indexes[typ] = idx
	}
	return idx
}

func refInput(v RefVerdict) string {
	return v.SourceType + "/" + v.SourceID + "#" + v.Field + "->" + v.TargetType + "/" + v.TargetID
}

func joinTypes(types []string) string {
	out := ""
	for i, t := range types {
		if i > 0 {
			out += ","
		}
		out += t
	}
	return out
}

// ExtractBlock returns the records of a block whose own integrity and count
// stages passed. This is the partial-usability guarantee: it works even when
// the aggregate view cannot be generated, because it asserts nothing about
// other blocks or about outgoing references. Callers that need graph-level
// guarantees must use LoadType/Aggregate.
func ExtractBlock(report *Report, typ string) ([]Record, bool) {
	br, ok := report.Blocks[typ]
	if !ok || br.Status != StatusOK {
		return nil, false
	}
	out := make([]Record, len(br.Records))
	copy(out, br.Records)
	return out, true
}

// requestFindings applies the rejection-priority chain against one or more
// requested types and returns all matched findings ordered by priority, then
// type, then source position.
func requestFindings(report *Report, requested []string) []Finding {
	var findings []Finding

	scope := make(map[string]bool, len(report.Types))
	for _, t := range report.Types {
		scope[t] = true
	}
	inScope := make([]string, 0, len(requested))
	for _, t := range requested {
		if !scope[t] {
			findings = append(findings, Finding{
				Kind: KindOutOfScope, Type: t,
				Message: fmt.Sprintf("requested type %q is not covered by this export", t),
			})
			continue
		}
		inScope = append(inScope, t)
	}
	if len(findings) > 0 {
		// Priority: an out-of-scope request short-circuits lower-priority
		// diagnoses for the request as a whole.
		return findings
	}

	for _, t := range inScope {
		br := report.Blocks[t]
		switch br.Status {
		case StatusChecksumFailed:
			findings = append(findings, Finding{
				Kind: KindChecksumFailed, Type: t,
				Message: fmt.Sprintf("block %q failed integrity verification", t),
			})
		case StatusCountMismatch:
			findings = append(findings, Finding{
				Kind: KindCountMismatch, Type: t,
				Message: fmt.Sprintf("block %q declared %d records but parsed %d; whole block untrusted",
					t, br.Declared, br.Actual),
			})
		}
	}

	for _, t := range inScope {
		br := report.Blocks[t]
		if br.Status != StatusOK {
			continue
		}
		for _, v := range br.RefVerdicts {
			switch v.Verdict {
			case RefDangling:
				findings = append(findings, Finding{
					Kind: KindDanglingRef, Type: t, Record: v.SourceID, Field: v.Field,
					Target:  v.TargetType,
					Message: fmt.Sprintf("dangling reference: %s", refInput(v)),
				})
			case RefTargetUntrusted:
				findings = append(findings, Finding{
					Kind: KindTargetUntrusted, Type: t, Record: v.SourceID, Field: v.Field,
					Target:  v.TargetType,
					Message: fmt.Sprintf("reference unverifiable, target block untrusted: %s", refInput(v)),
				})
			}
		}
	}
	sortFindings(findings)
	return findings
}

var findingRank = map[ErrorKind]int{
	KindOutOfScope:      0,
	KindChecksumFailed:  1,
	KindCountMismatch:   2,
	KindDanglingRef:     3,
	KindTargetUntrusted: 4,
}

func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		if findingRank[fs[i].Kind] != findingRank[fs[j].Kind] {
			return findingRank[fs[i].Kind] < findingRank[fs[j].Kind]
		}
		if fs[i].Type != fs[j].Type {
			return fs[i].Type < fs[j].Type
		}
		if fs[i].Record != fs[j].Record {
			return fs[i].Record < fs[j].Record
		}
		return fs[i].Field < fs[j].Field
	})
}

func resolveRequestTypes(report *Report, types []string) []string {
	if len(types) > 0 {
		return types
	}
	return append([]string(nil), report.Types...)
}

// LoadType returns the records of one type when the block passes checksum and
// count stages and every outgoing reference resolves. Rejections carry the
// exact block and reason, ordered by rejection priority.
func (l *Loader) LoadType(ctx context.Context, dir, typ string, logger *DecisionLogger) ([]Record, error) {
	got, err := l.Aggregate(ctx, dir, []string{typ}, logger)
	if err != nil {
		return nil, err
	}
	return got[typ], nil
}

// Aggregate returns records of all requested types (all covered types when
// types is empty). The aggregate view exists only if every participating
// block is checksum-valid, count-consistent and all of their outgoing
// references resolve. Partial availability is unaffected: use ExtractBlock
// on the Report for individually verified blocks.
func (l *Loader) Aggregate(ctx context.Context, dir string, types []string, logger *DecisionLogger) (map[string][]Record, error) {
	report, err := l.Verify(ctx, dir, logger)
	if err != nil {
		return nil, err
	}
	requested := resolveRequestTypes(report, types)
	if findings := requestFindings(report, requested); len(findings) > 0 {
		return nil, &LoadError{Findings: findings}
	}
	out := make(map[string][]Record, len(requested))
	for _, t := range requested {
		recs, ok := ExtractBlock(report, t)
		if !ok {
			return nil, &LoadError{Findings: []Finding{{
				Kind: KindChecksumFailed, Type: t,
				Message: fmt.Sprintf("block %q unexpectedly unavailable", t),
			}}}
		}
		out[t] = recs
	}
	return out, nil
}
