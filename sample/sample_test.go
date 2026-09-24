package sample_test

import (
	"errors"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/mask"
	"ontology/record"
	"ontology/rule"
	"ontology/sample"
	"ontology/sink"
)

func build(chains, per int) []*record.Record {
	var recs []*record.Record
	for c := 0; c < chains; c++ {
		for i := 0; i < per; i++ {
			r, _ := record.New(chainID(c), record.LevelInfo, map[string]any{"i": float64(i)})
			recs = append(recs, r)
		}
	}
	return recs
}

func chainID(c int) string { return "chain-" + string(rune('a'+c/26)) + string(rune('a'+c%26)) }

func TestChainConsistencyAndRates(t *testing.T) {
	recs := build(50, 20)
	rand.New(rand.NewSource(7)).Shuffle(len(recs), func(i, j int) { recs[i], recs[j] = recs[j], recs[i] })
	cases := []struct {
		rate             float64
		wantMin, wantMax int
	}{
		{0, 0, 0},
		{0.5, 1, 999},
		{1, 1000, 1000},
	}
	for _, tc := range cases {
		sm := sample.New(tc.rate)
		verdict := map[string]bool{}
		kept := 0
		for _, r := range recs {
			d := sm.Decide(r.TraceID, r.Level)
			if prev, seen := verdict[r.TraceID]; seen && prev != d.Keep {
				t.Fatalf("rate %v: chain %s split", tc.rate, r.TraceID)
			}
			verdict[r.TraceID] = d.Keep
			if d.Keep {
				kept++
			}
		}
		if kept < tc.wantMin || kept > tc.wantMax {
			t.Fatalf("rate %v: kept %d outside [%d,%d]", tc.rate, kept, tc.wantMin, tc.wantMax)
		}
		if sm.HashCount() != int64(len(recs)) {
			t.Fatalf("rate %v: hashes %d != records %d", tc.rate, sm.HashCount(), len(recs))
		}
	}
}

func TestDecideDeterminism(t *testing.T) {
	sm := sample.New(0.5)
	for _, id := range []string{chainID(3), chainID(17), ""} {
		first := sm.Decide(id, record.LevelInfo)
		for i := 0; i < 19; i++ {
			if d := sm.Decide(id, record.LevelInfo); d != first {
				t.Fatalf("id %q: decision changed on repeat %d", id, i)
			}
		}
	}
	before := sm.HashCount()
	sm.Decide("x", record.LevelInfo)
	if got := sm.HashCount() - before; got != 1 {
		t.Fatalf("hashes per decision = %d, want 1", got)
	}
}

func TestErrorForcedAndEmptyID(t *testing.T) {
	sm := sample.New(0.5)
	var dropped, kept string
	for i := 0; dropped == "" || kept == ""; i++ {
		id := chainID(i)
		if sm.Decide(id, record.LevelInfo).Keep {
			kept = id
		} else {
			dropped = id
		}
	}
	cases := []struct {
		name              string
		id                string
		lvl               record.Level
		wantKeep, wantFrc bool
	}{
		{"dropped-chain-info", dropped, record.LevelInfo, false, false},
		{"dropped-chain-error", dropped, record.LevelError, true, true},
		{"dropped-chain-fatal", dropped, record.LevelFatal, true, true},
		{"kept-chain-error", kept, record.LevelError, true, false},
		{"kept-chain-warn", kept, record.LevelWarn, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := sm.Decide(tc.id, tc.lvl)
			if d.Keep != tc.wantKeep || d.Forced != tc.wantFrc {
				t.Fatalf("got %+v", d)
			}
		})
	}
	if d := sample.New(0).Decide("", record.LevelInfo); d.Keep {
		t.Fatal("rate 0 must drop empty-ID info record")
	}
	if d := sample.New(0).Decide("", record.LevelError); !d.Keep || !d.Forced {
		t.Fatal("rate 0 must force-keep empty-ID error record")
	}
	if d := sample.New(1).Decide("", record.LevelInfo); !d.Keep || d.Forced {
		t.Fatal("rate 1 must keep empty-ID record unforced")
	}
}

func pipeline(recs []*record.Record, sm *sample.Sampler, set *rule.Set) []string {
	var out []string
	for _, r := range recs {
		d := sm.Decide(r.TraceID, r.Level)
		if !d.Keep {
			continue
		}
		m := mask.Apply(r, set)
		if d.Forced {
			m.Fields["chain_incomplete"] = true
		}
		b, _ := m.Encode()
		out = append(out, string(b))
	}
	return out
}

func TestConcurrentEqualsSerial(t *testing.T) {
	set, _ := rule.Compile([]rule.Rule{{Pattern: "^SECRET-", Action: rule.ActionHash}})
	recs := build(50, 20)
	for i, r := range recs {
		if i%7 == 0 {
			r.Level = record.LevelError
		}
		r.Fields["tok"] = "SECRET-x"
	}
	serial := pipeline(recs, sample.New(0.5), set)
	sm := sample.New(0.5)
	var mu sync.Mutex
	var out []string
	var idx atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(idx.Add(1)) - 1
				if i >= len(recs) {
					return
				}
				got := pipeline(recs[i:i+1], sm, set)
				mu.Lock()
				out = append(out, got...)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	sort.Strings(out)
	sort.Strings(serial)
	if len(out) != len(serial) {
		t.Fatalf("counts differ: %d vs %d", len(out), len(serial))
	}
	for i := range out {
		if out[i] != serial[i] {
			t.Fatalf("element %d differs", i)
		}
	}
}

func sinkRecords(t *testing.T) ([]*record.Record, []byte) {
	t.Helper()
	recs := []*record.Record{
		{TraceID: "a", Level: record.LevelInfo, Fields: map[string]any{"k": "v1"}},
		{TraceID: "b", Level: record.LevelError, Fields: map[string]any{"k": "v2", "x": 1.0}},
		{TraceID: "c", Level: record.LevelDebug, Fields: map[string]any{}},
	}
	data, err := sink.Encode(recs)
	if err != nil {
		t.Fatal(err)
	}
	return recs, data
}

func TestTruncationClassification(t *testing.T) {
	recs, data := sinkRecords(t)
	type seg struct{ start, prefixEnd, end int }
	var segs []seg
	off := len(sink.Magic)
	for _, r := range recs {
		b, _ := r.Encode()
		segs = append(segs, seg{off, off + 8, off + 8 + len(b)})
		off += 8 + len(b)
	}
	if off != len(data) {
		t.Fatalf("layout mismatch: %d != %d", off, len(data))
	}
	counts := map[error]int{}
	for cut := 1; cut < len(data); cut++ {
		wantN, wantErr := 0, error(nil)
		if cut < len(sink.Magic) {
			wantErr = sink.ErrHeader
		} else {
			for _, sg := range segs {
				if cut >= sg.end {
					wantN++
					continue
				}
				switch avail := cut - sg.start; {
				case avail == 0:
				case avail < 8:
					wantErr = sink.ErrPrefix
				default:
					wantErr = sink.ErrBody
				}
				break
			}
		}
		got, err := sink.Parse(data[:cut])
		if !errors.Is(err, wantErr) || len(got) != wantN {
			t.Fatalf("cut %d: got (%d, %v), want (%d, %v)", cut, len(got), err, wantN, wantErr)
		}
		counts[wantErr]++
	}
	if counts[sink.ErrHeader] == 0 || counts[sink.ErrPrefix] == 0 || counts[sink.ErrBody] == 0 {
		t.Fatalf("missing truncation class: %v", counts)
	}
	full, err := sink.Parse(data)
	if err != nil || len(full) != len(recs) {
		t.Fatalf("full parse: %d records, %v", len(full), err)
	}
}

func TestCorruptionAndRoundTrip(t *testing.T) {
	recs, data := sinkRecords(t)
	bad := append([]byte(nil), data...)
	bad[len(sink.Magic)+8] ^= 0xFF
	if _, err := sink.Parse(bad); !errors.Is(err, sink.ErrCRC) {
		t.Fatalf("bit flip: got %v, want ErrCRC", err)
	}
	bad = append([]byte(nil), data...)
	bad[0] = 'X'
	if _, err := sink.Parse(bad); !errors.Is(err, sink.ErrHeader) {
		t.Fatalf("bad magic: got %v, want ErrHeader", err)
	}
	path := t.TempDir() + "/out.sink"
	if err := sink.WriteFile(path, recs); err != nil {
		t.Fatal(err)
	}
	back, err := sink.ReadFile(path)
	if err != nil || len(back) != len(recs) {
		t.Fatalf("round trip: %d, %v", len(back), err)
	}
	for i := range recs {
		b1, _ := recs[i].Encode()
		b2, _ := back[i].Encode()
		if string(b1) != string(b2) {
			t.Fatalf("record %d mismatch", i)
		}
	}
}
