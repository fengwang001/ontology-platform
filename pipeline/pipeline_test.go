package pipeline

import (
	"errors"
	"fmt"
	"math"
	"ontology/record"
	"ontology/spill"
	"os"
	"path/filepath"
	"testing"
)

func ingestN(t *testing.T, p *Pipeline, n int) int64 {
	t.Helper()
	var total int64
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("k%08d", (i*2654435761)%1000003)
		val := []byte(fmt.Sprintf("v%d", i))
		total += int64(record.Record{Key: key, Value: val}.EncodedLen())
		if err := p.Ingest(key, val); err != nil {
			t.Fatal(err)
		}
	}
	return total
}

func readOutput(t *testing.T, path string) []record.Record {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := spill.ReadRun(f)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func assertSorted(t *testing.T, recs []record.Record) {
	t.Helper()
	for i := 1; i < len(recs); i++ {
		if record.Less(recs[i], recs[i-1]) {
			t.Fatalf("output not sorted at %d", i)
		}
	}
}

// TestResidentBound: 200k records with a 1/50 budget; runs >= 40 and the
// historical max resident bytes never exceeds the limit. Also asserts
// output completeness and the merge comparison bound.
func TestResidentBound(t *testing.T) {
	dir := t.TempDir()
	const n = 200000
	probe, err := Open(filepath.Join(dir, "probe"), 1<<60)
	if err != nil {
		t.Fatal(err)
	}
	total := ingestN(t, probe, n)
	limit := total / 50
	p, err := Open(filepath.Join(dir, "p"), limit)
	if err != nil {
		t.Fatal(err)
	}
	ingestN(t, p, n)
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if p.MaxResident() > limit {
		t.Fatalf("max resident %d exceeds limit %d", p.MaxResident(), limit)
	}
	if p.RunCount() < 40 {
		t.Fatalf("runs=%d want >= 40", p.RunCount())
	}
	out := readOutput(t, p.OutputPath())
	if int64(len(out)) != p.Ingested() || int64(len(out)) != n {
		t.Fatalf("output %d, ingested %d, want %d", len(out), p.Ingested(), n)
	}
	assertSorted(t, out)
	k := p.RunCount()
	bound := 4 * int64(n) * int64(math.Ceil(math.Log2(float64(k)+1)))
	if p.Comparisons() > bound {
		t.Fatalf("comparisons %d exceed bound %d (K=%d)", p.Comparisons(), bound, k)
	}
}

func TestEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		recs []record.Record
	}{
		{"zero records", nil},
		{"single record", []record.Record{{Key: "a", Value: []byte("1")}}},
		{"all keys equal", []record.Record{
			{Key: "x", Value: []byte("1")}, {Key: "x", Value: []byte("2")},
			{Key: "x", Value: []byte("3")}}},
		{"empty key", []record.Record{{Key: "", Value: []byte("v")}, {Key: "a"}}},
		{"empty value", []record.Record{{Key: "a", Value: []byte{}}, {Key: "b"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Open(t.TempDir(), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range tc.recs {
				if err := p.Ingest(r.Key, r.Value); err != nil {
					t.Fatal(err)
				}
			}
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
			out := readOutput(t, p.OutputPath())
			if len(out) != len(tc.recs) {
				t.Fatalf("output %d want %d", len(out), len(tc.recs))
			}
			assertSorted(t, out)
			for i := 1; i < len(out); i++ {
				if out[i].Key == out[i-1].Key && out[i].Seq < out[i-1].Seq {
					t.Fatalf("equal keys not in arrival order at %d", i)
				}
			}
		})
	}
}

func TestOversizedRecord(t *testing.T) {
	p, err := Open(t.TempDir(), 16)
	if err != nil {
		t.Fatal(err)
	}
	err = p.Ingest("this-key-is-way-too-long", []byte("and a value too"))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err=%v want ErrTooLarge", err)
	}
	if err := p.Ingest("a", nil); err != nil {
		t.Fatalf("pipeline must survive oversized record: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	out := readOutput(t, p.OutputPath())
	if len(out) != 1 {
		t.Fatalf("output %d want 1", len(out))
	}
}
