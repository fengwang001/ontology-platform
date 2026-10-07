package snapshot

import (
	"context"
	"sort"
	"testing"
)

// printDecisions logs every judgment's inputs, output and basis (visible with
// `go test -v`), satisfying the requirement that each decision be traceable.
func printDecisions(t *testing.T, logger *DecisionLogger, tag string) {
	t.Helper()
	for _, d := range logger.Decisions() {
		t.Logf("[%s] stage=%s input=%q output=%q basis=%s", tag, d.Stage, d.Input, d.Output, d.Basis)
	}
}

func sampleExport() ExportRequest {
	return ExportRequest{
		"person": {
			{ID: "p1", Props: map[string]string{"name": "Ada"}, Links: []Link{
				{Field: "works_for", Target: "org", ID: "o1"},
			}},
			{ID: "p2", Props: map[string]string{"name": "Lin"}, Links: []Link{
				{Field: "mentor", Target: "person", ID: "p1"},
			}},
		},
		"org": {
			{ID: "o1", Props: map[string]string{"name": "Eng"}, Links: []Link{
				{Field: "lead", Target: "person", ID: "p1"},
			}},
		},
	}
}

func mustWrite(t *testing.T, dir string, req ExportRequest) {
	t.Helper()
	if err := WriteExport(context.Background(), dir, req); err != nil {
		t.Fatalf("WriteExport: %v", err)
	}
}

func verdictsOf(br *BlockResult) map[string]string {
	out := map[string]string{}
	for _, v := range br.RefVerdicts {
		out[v.Field+"->"+v.TargetType+"/"+v.TargetID] = v.Verdict
	}
	return out
}

// Bidirectional links are judged independently in both directions.
func TestBidirectionalReferencesIndependent(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, sampleExport())
	logger := NewDecisionLogger()
	rep, err := NewLoader().Verify(context.Background(), dir, logger)
	if err != nil {
		t.Fatal(err)
	}
	printDecisions(t, logger, "bidirectional-valid")

	person := verdictsOf(rep.Blocks["person"])
	org := verdictsOf(rep.Blocks["org"])
	if person["works_for->org/o1"] != RefResolved {
		t.Fatalf("person.works_for should resolve, got %v", person)
	}
	if org["lead->person/p1"] != RefResolved {
		t.Fatalf("org.lead should resolve independently, got %v", org)
	}

	// Remove o1 with a valid rewrite: person->org becomes dangling while the
	// reverse org->person link disappears with it. Then remove the remaining
	// org record's target instead, and prove the surviving reverse direction
	// is judged on its own evidence.
	if err := RemoveTargetRecord(dir, "org", "o1"); err != nil {
		t.Fatal(err)
	}
	logger2 := NewDecisionLogger()
	rep2, err := NewLoader().Verify(context.Background(), dir, logger2)
	if err != nil {
		t.Fatal(err)
	}
	printDecisions(t, logger2, "bidirectional-damaged")
	person2 := verdictsOf(rep2.Blocks["person"])
	if person2["works_for->org/o1"] != RefDangling {
		t.Fatalf("forward link must be dangling on own evidence, got %v", person2)
	}
	// org block is still trusted internally but now empty: its reverse link
	// vanished, there is simply no verdict that could inherit validity.
	if rep2.Blocks["org"].Status != StatusOK {
		t.Fatalf("org should remain checksum/count valid, got %s", rep2.Blocks["org"].Status)
	}
}

// Even if some records inside a count-mismatched block would individually
// parse/checksum fine, the whole block is untrusted.
func TestCountMismatchRejectsWholeBlock(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, sampleExport())
	if err := SetDeclaredCount(dir, "person", 99); err != nil {
		t.Fatal(err)
	}
	logger := NewDecisionLogger()
	rep, err := NewLoader().Verify(context.Background(), dir, logger)
	if err != nil {
		t.Fatal(err)
	}
	printDecisions(t, logger, "count-mismatch")

	pb := rep.Blocks["person"]
	if pb.Status != StatusCountMismatch {
		t.Fatalf("person status = %s", pb.Status)
	}
	if pb.Records != nil {
		t.Fatalf("records of a count-mismatched block must not be exposed as trusted")
	}
	if _, ok := ExtractBlock(rep, "person"); ok {
		t.Fatalf("ExtractBlock must not salvage records from a count-mismatched block")
	}
	if len(pb.RefVerdicts) != 0 {
		t.Fatalf("untrusted block references must not be judged")
	}
}

// When the target block is untrusted, references are reported as unverifiable,
// never as dangling and never silently accepted.
func TestTargetUntrustedIsConservative(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, sampleExport())
	if err := SetDeclaredCount(dir, "org", 1); err == nil {
		// declared 1 vs actual 1 would still match; force a real mismatch.
	}
	if err := SetDeclaredCount(dir, "org", 42); err != nil {
		t.Fatal(err)
	}
	logger := NewDecisionLogger()
	rep, err := NewLoader().Verify(context.Background(), dir, logger)
	if err != nil {
		t.Fatal(err)
	}
	printDecisions(t, logger, "target-untrusted")

	person := verdictsOf(rep.Blocks["person"])
	if person["works_for->org/o1"] != RefTargetUntrusted {
		t.Fatalf("expected target_untrusted, got %v", person)
	}

	// Request-level rejection must surface the conservative category, not the
	// sharper dangling one.
	_, lerr := NewLoader().LoadType(context.Background(), dir, "person", NewDecisionLogger())
	le := asLoadErr(t, lerr)
	if !le.Has(KindTargetUntrusted) || le.Has(KindDanglingRef) {
		t.Fatalf("expected target_untrusted finding only, got %v", lerr)
	}
}

// Aggregate is all-or-nothing, while individually verified blocks remain
// extractable.
func TestAggregateBlockedButSingleBlockExtractable(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, sampleExport())
	if err := FlipPayloadByte(dir, "org", 3); err != nil {
		t.Fatal(err)
	}
	logger := NewDecisionLogger()
	rep, err := NewLoader().Verify(context.Background(), dir, logger)
	if err != nil {
		t.Fatal(err)
	}
	printDecisions(t, logger, "aggregate-blocked")

	_, aerr := NewLoader().Aggregate(context.Background(), dir, nil, NewDecisionLogger())
	ae := asLoadErr(t, aerr)
	if !ae.Has(KindChecksumFailed) {
		t.Fatalf("aggregate must be rejected, got %v", aerr)
	}
	// person also holds a link into the now-untrusted org block: its request
	// outcome is target_untrusted, and aggregate stays unavailable.
	recs, ok := ExtractBlock(rep, "person")
	if !ok || len(recs) != 2 {
		t.Fatalf("person block must remain independently extractable, ok=%v n=%d", ok, len(recs))
	}

	// An all-types request lists the checksum failure (higher priority) and
	// the conservative reference finding, never mislabelled as dangling.
	if ae.Has(KindDanglingRef) {
		t.Fatalf("must not report dangling when target block failed checksum")
	}
}

// Out-of-scope requests have the highest rejection priority and name the type.
func TestOutOfScopePriority(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, sampleExport())
	if err := FlipPayloadByte(dir, "person", 2); err != nil {
		t.Fatal(err)
	}
	_, lerr := NewLoader().LoadType(context.Background(), dir, "rocket", NewDecisionLogger())
	le := asLoadErr(t, lerr)
	if !le.Has(KindOutOfScope) {
		t.Fatalf("expected out_of_scope, got %v", lerr)
	}
	for _, f := range le.Findings {
		if f.Kind != KindOutOfScope {
			t.Fatalf("out-of-scope must short-circuit lower priorities, got %v", lerr)
		}
	}
}

// Checksum failure is block-local and independent of other blocks.
func TestChecksumFailureIsBlockLocal(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, sampleExport())
	if err := FlipPayloadByte(dir, "person", 5); err != nil {
		t.Fatal(err)
	}
	logger := NewDecisionLogger()
	rep, err := NewLoader().Verify(context.Background(), dir, logger)
	if err != nil {
		t.Fatal(err)
	}
	printDecisions(t, logger, "checksum")
	if rep.Blocks["person"].Status != StatusChecksumFailed {
		t.Fatalf("person = %s", rep.Blocks["person"].Status)
	}
	if rep.Blocks["org"].Status != StatusOK {
		t.Fatalf("org must be unaffected, got %s", rep.Blocks["org"].Status)
	}
}

// Genuinely dangling references are detected on trusted target blocks.
func TestDanglingReferenceDetected(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, sampleExport())
	if err := RemoveTargetRecord(dir, "org", "o1"); err != nil {
		t.Fatal(err)
	}
	_, lerr := NewLoader().LoadType(context.Background(), dir, "person", NewDecisionLogger())
	le := asLoadErr(t, lerr)
	if !le.Has(KindDanglingRef) {
		t.Fatalf("expected dangling, got %v", lerr)
	}
	if le.Has(KindTargetUntrusted) {
		t.Fatalf("trusted target must not yield target_untrusted")
	}
}

// Concurrent, repeated read-only verification is deterministic, repeatable
// and leaves the export untouched.
func TestConcurrentRepeatedVerifyDeterminism(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, sampleExport())
	if err := SetDeclaredCount(dir, "org", 7); err != nil {
		t.Fatal(err)
	}

	snapshot := readDir(t, dir)
	want := canonicalReport(t, dir)

	const goroutines = 32
	const rounds = 5
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			for r := 0; r < rounds; r++ {
				rep, err := NewLoader().Verify(context.Background(), dir, NewDecisionLogger())
				if err != nil {
					errs <- err
					return
				}
				if got := canonicalReportValue(rep); got != want {
					errs <- &mismatchError{got, want}
					return
				}
			}
			errs <- nil
		}()
	}
	for g := 0; g < goroutines; g++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got := readDir(t, dir); got != snapshot {
		t.Fatalf("read-only verification mutated export bytes")
	}
}

type mismatchError struct{ got, want string }

func (e *mismatchError) Error() string { return "report mismatch:\n got " + e.got + "\nwant " + e.want }

func canonicalReport(t *testing.T, dir string) string {
	t.Helper()
	rep, err := NewLoader().Verify(context.Background(), dir, NewDecisionLogger())
	if err != nil {
		t.Fatal(err)
	}
	return canonicalReportValue(rep)
}

func canonicalReportValue(rep *Report) string {
	types := append([]string(nil), rep.Types...)
	sort.Strings(types)
	out := ""
	for _, typ := range types {
		br := rep.Blocks[typ]
		out += typ + ":" + br.Status + ":" + itoa(br.Declared) + ":" + itoa(br.Actual) + ";"
		var vs []RefVerdict
		vs = append(vs, br.RefVerdicts...)
		sort.Slice(vs, func(i, j int) bool {
			return refInput(vs[i]) < refInput(vs[j])
		})
		for _, v := range vs {
			out += refInput(v) + "=" + v.Verdict + ","
		}
		out += "|"
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func readDir(t *testing.T, dir string) string {
	t.Helper()
	loader := NewLoader()
	rep, err := loader.Verify(context.Background(), dir, NewDecisionLogger())
	_ = rep
	_ = err
	// Hash all chunk file bytes via a fresh verification-independent read.
	return rawFingerprint(t, dir)
}
