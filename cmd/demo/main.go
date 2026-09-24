// Command demo exercises the idempotent, resumable batch importer end to end.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"time"

	"ontology/batch"
	"ontology/importer"
	"ontology/progress"
	"ontology/reconcile"
	"ontology/store"
)

var checks, failures int

func report(name, detail string, ok bool) {
	checks++
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

// checkTruncation verifies the three decidable truncation classes.
func checkTruncation(dir string) {
	p := &progress.Progress{BatchID: "b1", Total: 30, Intervals: [][2]int{{0, 10}, {10, 20}, {20, 30}}}
	data := progress.Marshal(p)
	var ends []int
	for i, b := range data {
		if b == '\n' {
			ends = append(ends, i+1)
		}
	}
	_, errH := progress.Parse(data[:ends[0]-2]) // inside header
	_, errI := progress.Parse(data[:ends[1]-2]) // inside an interval line
	_, errC := progress.Parse(data[:len(data)-2])
	ok := errors.Is(errH, progress.ErrHeader) &&
		errors.Is(errI, progress.ErrInterval) &&
		errors.Is(errC, progress.ErrCRC)
	report("progress-truncation-classes", "header/interval/crc each decidable", ok)
}

func main() {
	dir, err := os.MkdirTemp("", "ontology-demo")
	if err != nil {
		fmt.Println("FAIL setup", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	checkResume(dir)
	checkRepeatImport(dir)
	checkStoreWins(dir)
	checkLock(dir)
	checkTruncation(dir)
	checkReconcile(dir)
	checkManifest()

	fmt.Printf("TOTAL %d/%d OK\n", checks-failures, checks)
	if failures > 0 {
		os.Exit(1)
	}
}

func mustBatch(id string, n int) *batch.Batch {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("%s-k%06d", id, i)
	}
	b, err := batch.New(id, keys)
	if err != nil {
		panic(err)
	}
	return b
}

// checkResume interrupts a 100k batch at record 70000 and verifies the
// resume rewrites no more than the flush granularity N.
func checkResume(dir string) {
	st := store.New()
	b := mustBatch("bulk", 100000)
	st.FailAt(70000)
	d := dir + "/resume"
	os.MkdirAll(d, 0o755)
	if err := importer.New(st, d, 1000).Run(b); err == nil {
		report("resume-after-70000", "no injected failure", false)
		return
	}
	im := importer.New(st, d, 1000)
	err := im.Run(b)
	ok := err == nil && im.ResumeRewrites() <= 1000 && st.Len() == 100000
	report("resume-after-70000", fmt.Sprintf("rewrites=%d<=%d len=%d", im.ResumeRewrites(), 1000, st.Len()), ok)
}

// checkRepeatImport verifies byte-identical store and already-committed
// result when importing the same batch twice.
func checkRepeatImport(dir string) {
	st := store.New()
	b := mustBatch("rep", 5000)
	d := dir + "/rep"
	os.MkdirAll(d, 0o755)
	if err := importer.New(st, d, 500).Run(b); err != nil {
		report("repeat-import", err.Error(), false)
		return
	}
	before := st.Bytes()
	err := importer.New(st, d, 500).Run(b)
	ok := errors.Is(err, importer.ErrAlreadyCommitted) && bytes.Equal(before, st.Bytes())
	report("repeat-import", "byte-identical + already-committed", ok)
}

// checkStoreWins crafts progress claiming 50000 while the store holds only
// 30000 records; the importer must resume from the store frontier.
func checkStoreWins(dir string) {
	st := store.New()
	b := mustBatch("skew", 100000)
	st.FailAt(30001) // exactly 30000 records in store
	d := dir + "/skew"
	os.MkdirAll(d, 0o755)
	importer.New(st, d, 1000).Run(b) // interrupted
	progress.Save(d, &progress.Progress{BatchID: "skew", Total: 100000, Intervals: [][2]int{{0, 50000}}})
	im := importer.New(st, d, 1000)
	err := im.Run(b)
	ok := err == nil && st.Len() == 100000 && im.ResumeRewrites() == 0
	report("store-beats-progress", fmt.Sprintf("resumed@30000 rewrites=%d len=%d", im.ResumeRewrites(), st.Len()), ok)
}

// checkLock verifies a live holder blocks concurrent import and an expired
// lock can be taken over.
func checkLock(dir string) {
	st := store.New()
	d := dir + "/lock"
	os.MkdirAll(d, 0o755)
	rel, _ := progress.Acquire(d, "lk", "holder-A", time.Now(), time.Minute)
	errBusy := importer.New(st, d, 10).Run(mustBatch("lk", 10))
	rel()
	progress.Acquire(d, "lk2", "holder-B", time.Now().Add(-2*time.Hour), time.Minute)
	errTake := importer.New(st, d, 10).Run(mustBatch("lk2", 10))
	ok := errors.Is(errBusy, importer.ErrBusy) && errTake == nil
	report("batch-lock", "busy rejected, expired taken over", ok)
}

// checkReconcile interrupts a batch mid-flight and verifies the
// written/gap/extra three-segment report and in-flight accounting.
func checkReconcile(dir string) {
	st := store.New()
	b := mustBatch("rec", 10)
	d := dir + "/rec"
	os.MkdirAll(d, 0o755)
	st.FailAt(5) // writes 0..3, fails the 5th put
	importer.New(st, d, 2).Run(b)
	st.Put("rec-k000006", batch.RecordValue("rec", "rec-k000006")) // 6 written out of band
	st.Put("ghost", batch.RecordValue("rec", "ghost"))             // extra record
	rep := reconcile.Run(b, st, progress.Committed(d, "rec"))
	gotSeg := fmt.Sprintf("written=%v gaps=%v extra=%v", rep.Written, rep.Gaps, rep.Extra)
	okSeg := len(rep.Written) == 2 && rep.Written[0] == [2]int{0, 4} && rep.Written[1] == [2]int{6, 7} &&
		len(rep.Gaps) == 2 && rep.Gaps[0] == [2]int{4, 6} && rep.Gaps[1] == [2]int{7, 10} &&
		len(rep.Extra) == 1 && rep.Extra[0] == "ghost"
	report("reconcile-segments", gotSeg, okSeg)
	okInFlight := rep.InFlightCount == 5 && rep.CommittedCount == 0
	report("inflight-not-committed", fmt.Sprintf("inflight=%d committed=%d", rep.InFlightCount, rep.CommittedCount), okInFlight)
}

// checkManifest round-trips a batch manifest through Encode/Decode.
func checkManifest() {
	b := mustBatch("mf", 3)
	b.Keys[1] = "" // empty business key is legal
	var buf bytes.Buffer
	if err := batch.Encode(&buf, b); err != nil {
		report("manifest-codec", err.Error(), false)
		return
	}
	got, err := batch.Decode(&buf)
	ok := err == nil && got.ID == b.ID && len(got.Keys) == 3 && got.Keys[1] == ""
	report("manifest-codec", "encode/decode round-trip incl. empty key", ok)
}
