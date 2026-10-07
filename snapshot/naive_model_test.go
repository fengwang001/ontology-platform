package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"testing"
)

// naiveReport is an intentionally independent, dead-simple reference
// implementation: every reference check performs a full linear scan over the
// target block, no shared indexes, no early block-state reuse beyond the bare
// minimum. It is written in the most literal reading of the specification so
// that randomised differential testing can catch any clever-but-wrong
// behaviour in the real implementation.
type naiveBlock struct {
	status   string
	declared int
	actual   int
	records  []Record
	verdicts []naiveVerdict
}

type naiveVerdict struct {
	key     string
	verdict string
}

func naiveVerify(t *testing.T, dir string) map[string]*naiveBlock {
	t.Helper()
	mb, err := os.ReadFile(dir + "/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var man manifest
	if err := json.Unmarshal(mb, &man); err != nil {
		t.Fatal(err)
	}
	out := map[string]*naiveBlock{}
	for _, typ := range man.Types {
		raw, err := os.ReadFile(chunkPath(dir, typ))
		if err != nil {
			out[typ] = &naiveBlock{status: StatusChecksumFailed}
			continue
		}
		var cf chunkFile
		if json.Unmarshal(raw, &cf) != nil || cf.Type != typ {
			out[typ] = &naiveBlock{status: StatusChecksumFailed}
			continue
		}
		payload, derr := hex.DecodeString(cf.Payload)
		sum := sha256.Sum256(payload)
		if derr != nil || hex.EncodeToString(sum[:]) != cf.Checksum {
			out[typ] = &naiveBlock{status: StatusChecksumFailed, declared: cf.Count}
			continue
		}
		var records []Record
		if json.Unmarshal(payload, &records) != nil {
			out[typ] = &naiveBlock{status: StatusChecksumFailed, declared: cf.Count}
			continue
		}
		b := &naiveBlock{status: StatusOK, declared: cf.Count, actual: len(records), records: records}
		if b.actual != b.declared {
			b.status = StatusCountMismatch
		}
		out[typ] = b
	}
	for _, typ := range man.Types {
		b := out[typ]
		if b.status != StatusOK {
			continue
		}
		for _, rec := range b.records {
			for _, link := range rec.Links {
				v := naiveVerdict{
					key: typ + "/" + rec.ID + "#" + link.Field + "->" + link.Target + "/" + link.ID,
				}
				tb, covered := out[link.Target]
				switch {
				case !covered:
					v.verdict = RefDangling
				case tb.status != StatusOK:
					v.verdict = RefTargetUntrusted
				default:
					found := false
					for _, tr := range tb.records { // deliberately O(N)
						if tr.ID == link.ID {
							found = true
							break
						}
					}
					if found {
						v.verdict = RefResolved
					} else {
						v.verdict = RefDangling
					}
				}
				b.verdicts = append(b.verdicts, v)
			}
		}
	}
	return out
}

// randomBuildExport constructs an export with random ids and links for a fixed
// seed. Roughly half of links point to truly nonexistent ids so dangling is
// exercised even without corruption.
func randomBuildExport(rng *rand.Rand) ExportRequest {
	typeNames := []string{"person", "org", "project", "role"}
	req := ExportRequest{}
	idPool := map[string][]string{}
	for _, typ := range typeNames {
		n := rng.Intn(6) + 1
		ids := make([]string, n)
		for i := range ids {
			ids[i] = fmt.Sprintf("%s-%d-%d", typ, rng.Intn(100000), i)
		}
		idPool[typ] = ids
	}
	for _, typ := range typeNames {
		for _, id := range idPool[typ] {
			rec := Record{ID: id, Props: map[string]string{"k": fmt.Sprint(rng.Intn(7))}}
			nLinks := rng.Intn(3)
			for i := 0; i < nLinks; i++ {
				target := typeNames[rng.Intn(len(typeNames))]
				var targetID string
				if rng.Intn(2) == 0 && len(idPool[target]) > 0 {
					targetID = idPool[target][rng.Intn(len(idPool[target]))]
				} else {
					targetID = fmt.Sprintf("ghost-%d", rng.Intn(100000))
				}
				rec.Links = append(rec.Links, Link{
					Field:  fmt.Sprintf("f%d", i),
					Target: target,
					ID:     targetID,
				})
			}
			req[typ] = append(req[typ], rec)
		}
	}
	return req
}

// applyRandomDamage performs one of several independently chosen damage
// classes (possibly more than one), covering every error category.
func applyRandomDamage(t *testing.T, rng *rand.Rand, dir string, req ExportRequest) {
	t.Helper()
	types := make([]string, 0, len(req))
	for typ := range req {
		types = append(types, typ)
	}
	sort.Strings(types)
	actions := rng.Intn(3) + 1
	for i := 0; i < actions; i++ {
		typ := types[rng.Intn(len(types))]
		switch rng.Intn(4) {
		case 0:
			cf, _, err := readChunk(dir, typ)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := hex.DecodeString(cf.Payload)
			if err != nil || len(payload) == 0 {
				continue
			}
			if err := FlipPayloadByte(dir, typ, rng.Intn(len(payload))); err != nil {
				t.Fatal(err)
			}
		case 1:
			if err := SetDeclaredCount(dir, typ, rng.Intn(50)); err != nil {
				t.Fatal(err)
			}
		case 2:
			recs := req[typ]
			if len(recs) == 0 {
				continue
			}
			_ = RemoveTargetRecord(dir, typ, recs[rng.Intn(len(recs))].ID)
		case 3:
			// No damage: leave a clean block.
		}
	}
}

// TestDifferentialVsNaiveModel fuzzes random export + random damage
// combinations and requires the real implementation to agree with the
// independent naive model on every block status and every link verdict.
func TestDifferentialVsNaiveModel(t *testing.T) {
	const iterations = 300
	for seed := int64(0); seed < iterations; seed++ {
		rng := rand.New(rand.NewSource(seed))
		dir := t.TempDir()
		req := randomBuildExport(rng)
		mustWrite(t, dir, req)
		applyRandomDamage(t, rng, dir, req)

		logger := NewDecisionLogger()
		rep, err := NewLoader().Verify(context.Background(), dir, logger)
		if err != nil {
			t.Fatalf("seed=%d verify: %v", seed, err)
		}
		want := naiveVerify(t, dir)

		for typ, wb := range want {
			gb := rep.Blocks[typ]
			if gb == nil {
				t.Fatalf("seed=%d missing block %s in real report", seed, typ)
			}
			if gb.Status != wb.status || gb.Declared != wb.declared {
				t.Fatalf("seed=%d block %s real=(%s,decl=%d,act=%d) naive=(%s,decl=%d,act=%d)",
					seed, typ, gb.Status, gb.Declared, gb.Actual, wb.status, wb.declared, wb.actual)
			}
			wv := map[string]string{}
			for _, v := range wb.verdicts {
				wv[v.key] = v.verdict
			}
			gv := map[string]string{}
			for _, v := range gb.RefVerdicts {
				gv[refInput(v)] = v.Verdict
			}
			if len(gv) != len(wv) {
				t.Fatalf("seed=%d block %s verdict count real=%d naive=%d", seed, typ, len(gv), len(wv))
			}
			for key, verdict := range wv {
				if gv[key] != verdict {
					printDecisions(t, logger, fmt.Sprintf("seed=%d", seed))
					t.Fatalf("seed=%d %s real=%q naive=%q", seed, key, gv[key], verdict)
				}
			}
		}
	}
}

// Probe instrumentation proves membership checks do not grow linearly with
// target-block size: average probes per Has must stay bounded as N increases
// by two orders of magnitude.
func TestMembershipLookupIsConstantProbeCount(t *testing.T) {
	sizes := []int{64, 1024, 16384}
	var prevAvg float64
	for _, n := range sizes {
		idx := newIDIndex(n)
		ids := make([]string, n)
		for i := 0; i < n; i++ {
			ids[i] = fmt.Sprintf("id-%08d", i)
			idx.Add(ids[i])
		}
		before := idx.ProbeCount()
		rng := rand.New(rand.NewSource(int64(n)))
		queries := 10000
		for i := 0; i < queries; i++ {
			// Half hits, half guaranteed misses.
			if i%2 == 0 {
				idx.Has(ids[rng.Intn(n)])
			} else {
				idx.Has(fmt.Sprintf("missing-%08d", rng.Intn(n)))
			}
		}
		avg := float64(idx.ProbeCount()-before) / float64(queries)
		t.Logf("N=%d average slot probes per Has=%.4f (must be O(1), not O(N))", n, avg)
		if avg > 3.0 {
			t.Fatalf("avg probes %.3f exceeds expected-constant bound at N=%d", avg, n)
		}
		if prevAvg > 0 && avg > prevAvg*1.5 {
			t.Fatalf("probe count grew with N: %.4f -> %.4f", prevAvg, avg)
		}
		prevAvg = avg
	}
}

// A hash index must never use a linear scan: even with N=16384 entries the
// total probes for one lookup is in the low single digits.
func TestSingleLookupProbeBound(t *testing.T) {
	const n = 16384
	idx := newIDIndex(n)
	for i := 0; i < n; i++ {
		idx.Add(fmt.Sprintf("present-%d", i))
	}
	before := idx.ProbeCount()
	idx.Has("definitely-absent")
	used := idx.ProbeCount() - before
	t.Logf("one miss among %d entries cost %d slot probes (linear scan would cost %d)", n, used, n)
	if used > 8 {
		t.Fatalf("single lookup used %d probes, expected constant", used)
	}
}
