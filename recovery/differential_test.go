package recovery

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// naiveAnalyze is an independent, straightforward reference implementation
// of the analysis-phase definition, used to differential-test Log.Analyze.
func naiveAnalyze(records []Record) Result {
	// Pass 1: map each BeginCkpt LSN to its EndCkpt record, if any.
	endByBegin := make(map[LSN]Record)
	for _, r := range records {
		if r.Kind == KindEndCkpt {
			endByBegin[r.Begin] = r
		}
	}
	// Pass 2: walk forward, remembering the last BeginCkpt that has a
	// matching EndCkpt (incomplete checkpoints are ignored).
	var startLSN LSN
	var snapshot Record
	complete := false
	for _, r := range records {
		if r.Kind == KindBeginCkpt {
			if ec, ok := endByBegin[r.LSN]; ok {
				startLSN = r.LSN
				snapshot = ec
				complete = true
			}
		}
	}
	// Pass 3: replay per the record-by-record definition.
	dpt := make(map[PageID]LSN)
	att := make(map[TxnID]ATTEntry)
	if complete {
		for page, recLSN := range snapshot.DPT {
			dpt[page] = recLSN
		}
		for txn, entry := range snapshot.ATT {
			att[txn] = entry
		}
	}
	for _, r := range records {
		if complete && r.LSN <= startLSN {
			continue
		}
		switch r.Kind {
		case KindUpdate:
			e, ok := att[r.Txn]
			if !ok {
				e = ATTEntry{Status: StatusRunning}
			}
			e.LastLSN = r.LSN
			att[r.Txn] = e
			if _, dirty := dpt[r.Page]; !dirty {
				dpt[r.Page] = r.LSN
			}
		case KindCommit:
			att[r.Txn] = ATTEntry{Status: StatusCommitted, LastLSN: r.LSN}
		case KindAbort:
			att[r.Txn] = ATTEntry{Status: StatusAborting, LastLSN: r.LSN}
		case KindEnd:
			delete(att, r.Txn)
		case KindPageFlush:
			delete(dpt, r.Page)
		}
	}
	// Render sorted result.
	pages := make([]int, 0, len(dpt))
	for p := range dpt {
		pages = append(pages, int(p))
	}
	sort.Ints(pages)
	res := Result{DPT: []DirtyPageEntry{}, ATT: []ActiveTxnEntry{}, Failed: []TxnID{}}
	for _, p := range pages {
		res.DPT = append(res.DPT, DirtyPageEntry{Page: PageID(p), RecLSN: dpt[PageID(p)]})
	}
	txns := make([]int, 0, len(att))
	for txn := range att {
		txns = append(txns, int(txn))
	}
	sort.Ints(txns)
	for _, txn := range txns {
		res.ATT = append(res.ATT, ActiveTxnEntry{Txn: TxnID(txn), Entry: att[TxnID(txn)]})
	}
	if len(res.DPT) > 0 {
		res.NeedRedo = true
		res.RedoLSN = res.DPT[0].RecLSN
		for _, e := range res.DPT {
			if e.RecLSN < res.RedoLSN {
				res.RedoLSN = e.RecLSN
			}
		}
	}
	for _, e := range res.ATT {
		if e.Entry.Status == StatusRunning || e.Entry.Status == StatusAborting {
			res.Failed = append(res.Failed, e.Txn)
		}
	}
	return res
}

// formatRecord renders a record compactly for test logs.
func formatRecord(r Record) string {
	switch r.Kind {
	case KindUpdate:
		return fmt.Sprintf("%d:Update(t%d,p%d)", r.LSN, r.Txn, r.Page)
	case KindCommit:
		return fmt.Sprintf("%d:Commit(t%d)", r.LSN, r.Txn)
	case KindAbort:
		return fmt.Sprintf("%d:Abort(t%d)", r.LSN, r.Txn)
	case KindEnd:
		return fmt.Sprintf("%d:End(t%d)", r.LSN, r.Txn)
	case KindBeginCkpt:
		return fmt.Sprintf("%d:BeginCkpt", r.LSN)
	case KindEndCkpt:
		dpt := make([]string, 0, len(r.DPT))
		for p, rec := range r.DPT {
			dpt = append(dpt, fmt.Sprintf("p%d:%d", p, rec))
		}
		sort.Strings(dpt)
		att := make([]string, 0, len(r.ATT))
		for txn, e := range r.ATT {
			att = append(att, fmt.Sprintf("t%d:(%s,%d)", txn, e.Status, e.LastLSN))
		}
		sort.Strings(att)
		return fmt.Sprintf("%d:EndCkpt(begin=%d,dpt={%s},att={%s})",
			r.LSN, r.Begin, strings.Join(dpt, ","), strings.Join(att, ","))
	case KindPageFlush:
		return fmt.Sprintf("%d:PageFlush(p%d)", r.LSN, r.Page)
	}
	return fmt.Sprintf("%d:?", r.LSN)
}

func formatRecords(recs []Record) string {
	parts := make([]string, len(recs))
	for i, r := range recs {
		parts[i] = formatRecord(r)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// genRandomLog builds a random valid log by construction: every generated
// record satisfies the Append rules, so Append must never reject it.
func genRandomLog(t *testing.T, rnd *rand.Rand, n int) *Log {
	t.Helper()
	l := NewLog()
	lsn := LSN(0)
	nextTxn := TxnID(1)
	var openTxns []TxnID // seen and not ended
	var pendingBegins []LSN

	for i := 0; i < n; i++ {
		lsn += LSN(1 + rnd.Intn(3))
		op := rnd.Intn(100)
		var rec Record
		switch {
		case op < 40: // Update
			var txn TxnID
			if len(openTxns) == 0 || rnd.Intn(2) == 0 {
				txn = nextTxn
				nextTxn++
				openTxns = append(openTxns, txn)
			} else {
				txn = openTxns[rnd.Intn(len(openTxns))]
			}
			rec = Update(lsn, txn, PageID(1+rnd.Intn(8)))
		case op < 52: // Commit
			if len(openTxns) == 0 {
				continue
			}
			rec = Commit(lsn, openTxns[rnd.Intn(len(openTxns))])
		case op < 62: // Abort
			if len(openTxns) == 0 {
				continue
			}
			rec = Abort(lsn, openTxns[rnd.Intn(len(openTxns))])
		case op < 72: // End
			if len(openTxns) == 0 {
				continue
			}
			idx := rnd.Intn(len(openTxns))
			rec = End(lsn, openTxns[idx])
			openTxns = append(openTxns[:idx], openTxns[idx+1:]...)
		case op < 80: // BeginCkpt
			rec = BeginCkpt(lsn)
			pendingBegins = append(pendingBegins, lsn)
		case op < 90: // EndCkpt for a pending begin, with a random snapshot
			if len(pendingBegins) == 0 {
				continue
			}
			idx := rnd.Intn(len(pendingBegins))
			begin := pendingBegins[idx]
			pendingBegins = append(pendingBegins[:idx], pendingBegins[idx+1:]...)
			dpt := make(map[PageID]LSN)
			for p := 1; p <= 8; p++ {
				if rnd.Intn(2) == 0 {
					dpt[PageID(p)] = LSN(rnd.Intn(int(lsn) + 1))
				}
			}
			att := make(map[TxnID]ATTEntry)
			for _, txn := range openTxns {
				if rnd.Intn(2) == 0 {
					att[txn] = ATTEntry{
						Status:  TxnStatus(rnd.Intn(3)),
						LastLSN: LSN(rnd.Intn(int(lsn) + 1)),
					}
				}
			}
			if rnd.Intn(3) == 0 { // a txn first appearing inside the snapshot
				txn := nextTxn
				nextTxn++
				openTxns = append(openTxns, txn)
				att[txn] = ATTEntry{
					Status:  TxnStatus(rnd.Intn(3)),
					LastLSN: LSN(rnd.Intn(int(lsn) + 1)),
				}
			}
			rec = EndCkpt(lsn, begin, dpt, att)
		default: // PageFlush
			rec = PageFlush(lsn, PageID(1+rnd.Intn(8)))
		}
		if err := l.Append(rec); err != nil {
			t.Fatalf("generated record rejected: %s: %v", formatRecord(rec), err)
		}
	}
	return l
}

// TestDifferentialRandomLogs runs 1000 random logs and compares Log.Analyze
// against the naive record-by-record reference implementation, logging the
// input, both outputs, and the verdict basis for every case.
func TestDifferentialRandomLogs(t *testing.T) {
	const cases = 1000
	seed := int64(20261001)
	rnd := rand.New(rand.NewSource(seed))
	for i := 0; i < cases; i++ {
		n := 1 + rnd.Intn(40)
		l := genRandomLog(t, rnd, n)
		records := l.Records()
		got := l.Analyze()
		want := naiveAnalyze(records)
		match := reflect.DeepEqual(got, want)
		t.Logf("case %d: input=%s", i, formatRecords(records))
		t.Logf("case %d: Log.Analyze=%+v", i, got)
		t.Logf("case %d: naiveAnalyze=%+v", i, want)
		t.Logf("case %d: verdict=%v basis=DeepEqual(Log.Analyze, naiveAnalyze) over the same appended record sequence", i, match)
		if !match {
			t.Fatalf("case %d mismatch:\ninput=%s\ngot =%+v\nwant=%+v", i, formatRecords(records), got, want)
		}
	}
}

// TestConcurrentAppendAnalyze hammers Append and Analyze from many
// goroutines; run with -race. The final analysis must equal the naive
// replay of the final record sequence (linearizability check).
func TestConcurrentAppendAnalyze(t *testing.T) {
	l := NewLog()
	var lsnCounter atomic.Int64
	var analyzers sync.WaitGroup
	var appenders sync.WaitGroup
	stop := make(chan struct{})

	// Analyzers: must never observe a torn state (race detector) and must
	// not mutate the log.
	for g := 0; g < 4; g++ {
		analyzers.Add(1)
		go func() {
			defer analyzers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = l.Analyze()
				}
			}
		}()
	}

	// Appenders: each owns a private txn id range, so only LSN ordering can
	// conflict; ErrLSNNotIncreasing rejections are retried with a fresh LSN.
	for g := 0; g < 4; g++ {
		appenders.Add(1)
		go func(g int) {
			defer appenders.Done()
			base := TxnID(1000 * (g + 1))
			for i := 0; i < 50; i++ {
				txn := base + TxnID(i)
				page := PageID(1 + (g*50+i)%16)
				appendRetry := func(make func(LSN) Record) {
					for {
						err := l.Append(make(LSN(lsnCounter.Add(1))))
						if err == nil {
							return
						}
						if !strings.Contains(err.Error(), ErrLSNNotIncreasing.Error()) {
							t.Errorf("unexpected append error: %v", err)
							return
						}
					}
				}
				appendRetry(func(lsn LSN) Record { return Update(lsn, txn, page) })
				appendRetry(func(lsn LSN) Record { return Commit(lsn, txn) })
				if i%3 == 0 {
					appendRetry(func(lsn LSN) Record { return PageFlush(lsn, page) })
				}
				appendRetry(func(lsn LSN) Record { return End(lsn, txn) })
			}
		}(g)
	}

	appenders.Wait()
	close(stop)
	analyzers.Wait()

	got := l.Analyze()
	want := naiveAnalyze(l.Records())
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("post-concurrency mismatch:\ngot =%+v\nwant=%+v", got, want)
	}
}
