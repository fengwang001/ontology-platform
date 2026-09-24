// Command demo runs acceptance checks; no args, no network.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"

	"ontology/mask"
	"ontology/record"
	"ontology/rule"
	"ontology/sample"
	"ontology/sink"
)

func rec(l record.Level, tid string, f record.Fields) *record.Record {
	r, err := record.New(l, tid, f)
	if err != nil {
		panic(err)
	}
	return r
}

func main() {
	fails := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK   " + name)
		} else {
			fmt.Println("FAIL " + name)
			fails++
		}
	}

	self := record.Fields{}
	self["loop"] = self
	_, cycleErr := record.New(record.Info, "t", self)
	enc, encErr := rec(record.Warn, "t", record.Fields{"k": "v"}).Encode()
	check("record: cycle rejected", errors.Is(cycleErr, record.ErrCycle))
	check("record: codec round trip", encErr == nil && len(enc) > 0)

	_, conflictErr := rule.Compile([]rule.Rule{
		{Path: "user.email", Action: rule.Replace},
		{Path: "user.email", Action: rule.Hash},
	})
	litSegs, _ := rule.ParsePath(`a\.b`)
	check("rule: same-path conflict detected", errors.Is(conflictErr, rule.ErrConflict))
	check("rule: literal-dot disambiguation", len(litSegs) == 1 && litSegs[0].Key == "a.b")

	mSet, _ := rule.Compile([]rule.Rule{
		{Pattern: "TOPSECRET", Action: rule.Replace},
		{Path: "nested.keep", Action: rule.Hash},
	})
	mk := mask.New(mSet)
	mrec := rec(record.Info, "t", record.Fields{
		"p1": "TOPSECRET", "p2": record.Fields{"p3": []any{"TOPSECRET"}},
		"nested": record.Fields{"keep": "shh", "other": "plain"},
	})
	before, _ := mrec.Encode()
	mout, merr := mk.Apply(mrec)
	after, _ := mout.Encode()
	allMasked := mout.Fields["p1"] == "***" &&
		mout.Fields["p2"].(record.Fields)["p3"].([]any)[0] == "***"
	want := strings.ReplaceAll(string(before), "TOPSECRET", "***")
	want = strings.Replace(want, `"keep":"shh"`,
		`"keep":"`+mout.Fields["nested"].(record.Fields)["keep"].(string)+`"`, 1)
	check("mask: same value masked at three paths", merr == nil && allMasked)
	check("mask: non-target fields byte-identical", string(after) == want)
	bigRules := make([]rule.Rule, 100)
	for i := range bigRules {
		bigRules[i] = rule.Rule{Path: fmt.Sprintf("f%d", i), Action: rule.Hash}
	}
	bigSet, _ := rule.Compile(bigRules)
	probe := []record.Seg{{Key: "f0", Index: -1}}
	for i := 0; i < 100000*4; i++ {
		bigSet.Lookup(probe)
	}
	check("mask: path lookups <= records*(leaves+const)", bigSet.MatchCount() <= uint64(100000*12))

	// sample: 50 chains x20 shuffled, deterministic, error force-keep marker.
	sd := sample.New(0.5)
	var chainRecs []*record.Record
	for c := 0; c < 50; c++ {
		for i := 0; i < 20; i++ {
			lvl := record.Info
			if c == 3 && i == 0 {
				lvl = record.Error
			}
			chainRecs = append(chainRecs, rec(lvl, fmt.Sprintf("trace-%02d", c), record.Fields{}))
		}
	}
	rand.Shuffle(len(chainRecs), func(i, j int) { chainRecs[i], chainRecs[j] = chainRecs[j], chainRecs[i] })
	decision := map[string]bool{}
	split, repeatOK, markerOK := false, true, true
	for _, r := range chainRecs {
		v := sd.Decide(r)
		if k, seen := decision[r.TraceID]; seen && k != v.ChainKept {
			split = true
		}
		decision[r.TraceID] = v.ChainKept
		for i := 0; i < 20; i++ {
			if sd.Keep(r.TraceID) != v.ChainKept {
				repeatOK = false
			}
		}
		out, vv := sd.Process(r)
		needMark := r.Level >= record.Error && !v.ChainKept
		if vv.ChainIncomplete != needMark || (needMark && (out == nil || !out.ChainIncomplete)) {
			markerOK = false
		}
	}
	check("sample: chains all-kept/all-dropped", !split)
	check("sample: same id 20x identical", repeatOK)
	check("sample: error force-kept + incomplete marker", markerOK)
	d0, d1 := sample.New(0), sample.New(1)
	i0, i0e, i1 := d0.Decide(rec(record.Info, "z", nil)), d0.Decide(rec(record.Error, "z", nil)), d1.Decide(rec(record.Info, "z", nil))
	check("sample: rate0/rate1 edge behavior", !i0.Keep && i0e.Keep && i0e.ChainIncomplete && i1.Keep)

	// concurrency vs serial.
	cc, cm := sample.New(0.5), mask.New(mSet)
	var in []*record.Record
	for i := 0; i < 200; i++ {
		in = append(in, rec(record.Level(i%5), fmt.Sprintf("tid-%d", i%40),
			record.Fields{"k": "TOPSECRET"}))
	}
	check("concurrency: parallel output equals serial",
		runSerial(cc, cm, in) == runParallel(cc, cm, in))

	// sink: full recovery + one cut per truncation class + crc.
	var buf bytes.Buffer
	sw, _ := sink.NewWriter(&buf)
	sw.Write(rec(record.Warn, "w", record.Fields{"i": int64(1)}))
	sw.Close()
	file := buf.Bytes()
	_, eFull := sink.ReadAll(file)
	_, eHdr := sink.ReadAll(file[:4])
	_, eLen := sink.ReadAll(file[:10])
	_, eBody := sink.ReadAll(file[:12])
	corrupt := bytes.Clone(file)
	corrupt[12] ^= 0xFF
	_, eCRC := sink.ReadAll(corrupt)
	check("sink: full file recovers record", eFull == nil)
	check("sink: header/length/body/crc classified",
		errors.Is(eHdr, sink.ErrHeader) && errors.Is(eLen, sink.ErrLengthPrefix) &&
			errors.Is(eBody, sink.ErrBodyTruncated) && errors.Is(eCRC, sink.ErrCRC))

	// depth.
	deep := record.Fields{"k": "v"}
	for i := 0; i < record.MaxDepth; i++ {
		deep = record.Fields{"k": deep}
	}
	_, depthErr := record.New(record.Info, "t", deep)
	check("record: depth exceeded rejected", errors.Is(depthErr, record.ErrDepthExceeded))

	if fails == 0 {
		fmt.Println("TOTAL: all checks passed")
		return
	}
	fmt.Printf("TOTAL: %d check(s) failed\n", fails)
}

func pipe(d *sample.Decider, mk *mask.Masker, r *record.Record) string {
	kept, _ := d.Process(r)
	if kept == nil {
		return "drop:" + r.TraceID + ":" + r.Level.String()
	}
	m, err := mk.Apply(kept)
	if err != nil {
		return "err"
	}
	b, _ := m.Encode()
	return string(b)
}

func runSerial(d *sample.Decider, mk *mask.Masker, in []*record.Record) string {
	out := make([]string, 0, len(in))
	for _, r := range in {
		out = append(out, pipe(d, mk, r))
	}
	sort.Strings(out)
	return strings.Join(out, "|")
}

func runParallel(d *sample.Decider, mk *mask.Masker, in []*record.Record) string {
	out := make([]string, len(in))
	var wg sync.WaitGroup
	for i, r := range in {
		wg.Add(1)
		go func(i int, r *record.Record) { defer wg.Done(); out[i] = pipe(d, mk, r) }(i, r)
	}
	wg.Wait()
	sort.Strings(out)
	return strings.Join(out, "|")
}
