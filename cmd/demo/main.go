// Command demo runs end-to-end acceptance checks for the sampling profiler.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"ontology/attrib"
	"ontology/dump"
	"ontology/sampler"
	"ontology/stack"
	"ontology/tree"
)

var checks, failures int

func report(ok bool, format string, args ...any) {
	checks++
	status := "OK  "
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, fmt.Sprintf(format, args...))
}

func mustStack(frames []string, maxDepth int) stack.Stack {
	s, err := stack.Normalize(frames, maxDepth)
	if err != nil {
		panic(err)
	}
	return s
}

func main() {
	tr := tree.New()
	stacks := [][]string{
		{"main", "A", "F", "G", "F", "H"},
		{"main", "A", "F", "G"},
		{"main", "B", "C"},
		{"main", "B", "C"},
	}
	for _, frames := range stacks {
		tr.Insert(mustStack(frames, 64))
	}
	snap := tr.Snapshot()

	selfSum, totalSum := snap.SelfSum(), snap.TotalSum()
	report(selfSum == tr.Samples() && totalSum > tr.Samples(),
		"self-sum==samples(%d) while total-sum(%d)>samples", selfSum, totalSum)

	rep := attrib.NewReport(snap)
	report(rep.FunctionTotal("F") == 2,
		"recursive F function-total=%d == outermost(2), not 3", rep.FunctionTotal("F"))

	tr2 := tree.New()
	deep := make([]string, 0, 9)
	for i := 0; i < 9; i++ {
		deep = append(deep, fmt.Sprintf("f%d", i))
	}
	tr2.Insert(mustStack(deep, 5))
	truncated := tr2.Snapshot().Root.Children[0].Children[0].Children[0].Children[0].Children[0]
	report(tr2.TruncatedSamples() == 1 && truncated.Truncated,
		"depth truncation counted=%d node flagged=%v", tr2.TruncatedSamples(), truncated.Truncated)

	tr3 := tree.New()
	const inserts, depth = 100000, 20
	frames := make([]string, depth)
	for i := range frames {
		frames[i] = fmt.Sprintf("g%d", i)
	}
	st := mustStack(frames, 64)
	for i := 0; i < inserts; i++ {
		tr3.Insert(st)
	}
	report(tr3.InsertOps() <= inserts*depth*4,
		"insert ops %d <= bound %d", tr3.InsertOps(), inserts*depth*4)

	tr4 := tree.New()
	var tick int
	now := int64(0)
	sm := sampler.New(sampler.Config{
		Interval: 10,
		Clock:    func() int64 { now += 10; return now },
		Source:   func() []string { return []string{"main", "work"} },
		Busy:     func() bool { return tick >= 500 && tick < 637 },
	}, tr4)
	for ; tick < 1000; tick++ {
		sm.Tick()
	}
	st4 := sm.Stats()
	report(tr4.Samples()+st4.Dropped+st4.Invalid == st4.Expected && st4.Dropped == 137,
		"drop identity: samples(%d)+dropped(%d)+invalid(%d)==expected(%d)",
		tr4.Samples(), st4.Dropped, st4.Invalid, st4.Expected)

	tr5 := tree.New()
	clock := int64(1000)
	script := []int64{0, 10, -50, 10, 10}
	sm5 := sampler.New(sampler.Config{
		Interval: 10,
		Clock:    func() int64 { d := script[0]; script = script[1:]; clock += d; return clock },
		Source:   func() []string { return []string{"main"} },
	}, tr5)
	for range 5 {
		sm5.Tick()
	}
	st5 := sm5.Stats()
	report(st5.Anomalous == 1 && tr5.Samples() == st5.Expected,
		"clock rollback counted anomalous=%d, identity holds", st5.Anomalous)

	buf := new(bytes.Buffer)
	if err := dump.Write(buf, snap); err != nil {
		panic(err)
	}
	data := buf.Bytes()
	recordsEnd := len(data) - 4
	_, errH := dump.Recover(data[:10])
	_, errR := dump.Recover(data[:(32+recordsEnd)/2])
	_, errC := dump.Recover(data[:len(data)-2])
	report(errors.Is(errH, dump.ErrHeaderIncomplete) &&
		errors.Is(errR, dump.ErrRecordIncomplete) &&
		errors.Is(errC, dump.ErrCRCMismatch),
		"truncation classes: header=%v record=%v crc=%v",
		errors.Is(errH, dump.ErrHeaderIncomplete),
		errors.Is(errR, dump.ErrRecordIncomplete),
		errors.Is(errC, dump.ErrCRCMismatch))

	rec, err := dump.Recover(data[:recordsEnd-10])
	consistent := err != nil && rec.SelfSum() == rec.Samples
	report(consistent, "recovered tree consistent: self-sum==recovered-samples(%d)",
		rec.Samples)

	fmt.Printf("OK   total %d/%d checks passed\n", checks-failures, checks)
	if failures != 0 {
		os.Exit(1)
	}
}
