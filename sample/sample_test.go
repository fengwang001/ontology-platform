package sample_test

import (
	"math/rand"
	"testing"

	"ontology/record"
	"ontology/sample"
)

// buildChainRecords creates 1000 records across 50 chains (20 each), shuffled.
func buildChainRecords() []*record.Record {
	rng := rand.New(rand.NewSource(42))
	var recs []*record.Record
	for c := 0; c < 50; c++ {
		tid := "chain-" + string(rune('a'+c/26)) + string(rune('a'+c%26))
		for i := 0; i < 20; i++ {
			lvl := record.Info
			if c == 7 && i == 3 { // one error on chain 7
				lvl = record.Error
			}
			recs = append(recs, mustRec(lvl, tid))
		}
	}
	rng.Shuffle(len(recs), func(i, j int) { recs[i], recs[j] = recs[j], recs[i] })
	return recs
}

func mustRec(l record.Level, tid string) *record.Record {
	r, _ := record.New(l, tid, record.Fields{"n": "v"})
	return r
}

func TestChainConsistency(t *testing.T) {
	rates := []float64{0, 0.5, 1}
	for _, rate := range rates {
		t.Run("rate", func(t *testing.T) {
			d := sample.New(rate)
			recs := buildChainRecords()

			// Every chain's records must agree on the chain decision, and the
			// same id must give the same answer over 20 repeated runs.
			chainKeep := map[string]bool{}
			for _, r := range recs {
				v := d.Decide(r)
				if k, seen := chainKeep[r.TraceID]; seen && k != v.ChainKept {
					t.Fatalf("rate %v: chain %q split decision", rate, r.TraceID)
				}
				chainKeep[r.TraceID] = v.ChainKept
				for i := 0; i < 20; i++ {
					if d.Keep(r.TraceID) != v.ChainKept {
						t.Fatalf("rate %v: id %q decision changed on repeat %d", rate, r.TraceID, i)
					}
				}
			}

			// Marker appears only when chain dropped but error force-kept.
			for _, r := range recs {
				out, v := d.Process(r)
				if v.ChainIncomplete != (r.Level >= record.Error && !chainKeep[r.TraceID]) {
					t.Fatalf("rate %v: marker condition wrong for %q", rate, r.TraceID)
				}
				if v.ChainIncomplete && (out == nil || !out.ChainIncomplete) {
					t.Fatalf("rate %v: marker not stamped", rate)
				}
				if r.Level < record.Error && !chainKeep[r.TraceID] && v.Keep {
					t.Fatalf("rate %v: non-error kept on dropped chain", rate)
				}
			}

			// Rate 0 drops every chain decision; rate 1 keeps every one.
			for _, k := range chainKeep {
				if rate == 0 && k {
					t.Fatal("rate 0 kept a chain")
				}
				if rate == 1 && !k {
					t.Fatal("rate 1 dropped a chain")
				}
			}
		})
	}
}

func TestEdgesAndHashCost(t *testing.T) {
	cases := []struct {
		name   string
		rate   float64
		tid    string
		level  record.Level
		kept   bool
		marker bool
	}{
		{"rate0 info dropped", 0, "abc", record.Info, false, false},
		{"rate0 error force kept marked", 0, "abc", record.Error, true, true},
		{"rate1 info kept", 1, "abc", record.Info, true, false},
		{"rate1 error kept no marker", 1, "abc", record.Error, true, false},
		{"empty trace info rate0", 0, "", record.Info, false, false},
		{"empty trace error rate0 marked", 0, "", record.Error, true, true},
		{"missing trace same as empty", 0, "", record.Warn, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := sample.New(tc.rate)
			before := d.HashCalls()
			v := d.Decide(mustRec(tc.level, tc.tid))
			calls := d.HashCalls() - before
			if calls != 1 {
				t.Fatalf("hash calls per decision = %d, want 1", calls)
			}
			if v.Keep != tc.kept || v.ChainIncomplete != tc.marker {
				t.Fatalf("got keep=%v marker=%v want %v/%v",
					v.Keep, v.ChainIncomplete, tc.kept, tc.marker)
			}
		})
	}

	// Empty and absent ids share one bucket => identical decisions.
	d := sample.New(0.5)
	if d.Keep("") != d.Keep("") {
		t.Fatal("empty trace id decisions inconsistent")
	}
}
