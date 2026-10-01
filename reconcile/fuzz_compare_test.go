package reconcile

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

type opKind int

const (
	opAddBank opKind = iota
	opAddBook
	opReconcile
	opReverse
)

type fuzzOp struct {
	kind opKind
	id   string
	amt  int64
	day  int64
	ref  string
	w    int64
	mid  int64
}

func runNaiveOps(ops []fuzzOp) (naive *naiveMatcher, reconcileOut [][]Match, revResults [][2]bool) {
	n := newNaive()
	for _, op := range ops {
		switch op.kind {
		case opAddBank:
			n.bank[op.id] = Line{ID: op.id, Amt: op.amt, Day: op.day, Ref: op.ref}
		case opAddBook:
			n.book[op.id] = Line{ID: op.id, Amt: op.amt, Day: op.day, Ref: op.ref}
		case opReconcile:
			reconcileOut = append(reconcileOut, n.reconcile(op.w))
		case opReverse:
			found, wasReversed := n.reverse(op.mid)
			if found {
				revResults = append(revResults, [2]bool{true, false})
			} else {
				revResults = append(revResults, [2]bool{false, wasReversed})
			}
		}
	}
	return n, reconcileOut, revResults
}

func runMatcherOps(t *testing.T, ops []fuzzOp) (*Matcher, []Match, [][2]bool) {
	t.Helper()
	m := NewMatcher()
	var reconcileConcat []Match
	var revResults [][2]bool
	for _, op := range ops {
		switch op.kind {
		case opAddBank:
			if err := m.AddLine(Bank, op.id, op.amt, op.day, op.ref); err != nil {
				t.Fatalf("unexpected add bank err: %v op=%#v", err, op)
			}
		case opAddBook:
			if err := m.AddLine(Book, op.id, op.amt, op.day, op.ref); err != nil {
				t.Fatalf("unexpected add book err: %v op=%#v", err, op)
			}
		case opReconcile:
			got, err := m.Reconcile(op.w)
			if err != nil {
				t.Fatalf("unexpected reconcile err: %v", err)
			}
			reconcileConcat = append(reconcileConcat, got...)
		case opReverse:
			_, _, err := m.Reverse(op.mid)
			switch {
			case err == nil:
				revResults = append(revResults, [2]bool{true, false})
			case errorIs(err, ErrMatchReversed):
				revResults = append(revResults, [2]bool{false, true})
			default:
				revResults = append(revResults, [2]bool{false, false})
			}
		}
	}
	return m, reconcileConcat, revResults
}

func errorIs(got, target error) bool { return got == target }

func matchKey(mm Match) string {
	return fmt.Sprintf("m%d|r%d|%v|%v", mm.Mid, mm.Round, mm.BankID, mm.BookID)
}

func assertSameState(t *testing.T, m *Matcher, n *naiveMatcher, label string) {
	t.Helper()
	gotM := m.Matches()
	wantM := n.matchList()
	if len(gotM) != len(wantM) {
		t.Fatalf("%s: match count %d != %d", label, len(gotM), len(wantM))
	}
	for i := range wantM {
		if matchKey(gotM[i]) != matchKey(wantM[i]) {
			t.Fatalf("%s: at %d got %+v want %+v", label, i, gotM[i], wantM[i])
		}
	}
	gotF := m.Forbidden()
	wantF := n.forbiddenList()
	if len(gotF) != len(wantF) {
		t.Fatalf("%s: forbid count %d != %d", label, len(gotF), len(wantF))
	}
	for i := range wantF {
		if gotF[i] != wantF[i] {
			t.Fatalf("%s: forbid at %d got %v want %v", label, i, gotF[i], wantF[i])
		}
	}
	if linesKey(unmatchedErr(t, m, Bank)) != linesKey(n.unmatched(Bank)) {
		t.Fatalf("%s: bank unmatched mismatch", label)
	}
	if linesKey(unmatchedErr(t, m, Book)) != linesKey(n.unmatched(Book)) {
		t.Fatalf("%s: book unmatched mismatch", label)
	}
}

func linesKey(ls []Line) string {
	var b strings.Builder
	for _, ln := range ls {
		fmt.Fprintf(&b, "%s:%d:%d:%q;", ln.ID, ln.Amt, ln.Day, ln.Ref)
	}
	return b.String()
}

// TestRandomCompareNaive 用 2000 组随机“登记/对账/撤销”序列，
// 与逐步书写的朴素模拟逐项对照；日志打印输入、输出与判定依据。
func TestRandomCompareNaive(t *testing.T) {
	const cases = 2000
	rng := rand.New(rand.NewSource(20261001))

	for c := 0; c < cases; c++ {
		ops := generateOps(rng)
		n, nReconOut, nRev := runNaiveOps(ops)
		m, mReconConcat, mRev := runMatcherOps(t, ops)

		// 朴素侧把每次 Reconcile 输出拼平。
		var nReconConcat []Match
		for _, batch := range nReconOut {
			nReconConcat = append(nReconConcat, batch...)
		}

		if len(mReconConcat) != len(nReconConcat) {
			t.Fatalf("case %d: reconcile output count %d != %d, ops=%s",
				c, len(mReconConcat), len(nReconConcat), opsString(ops))
		}
		for i := range nReconConcat {
			if matchKey(mReconConcat[i]) != matchKey(nReconConcat[i]) {
				t.Fatalf("case %d: produced[%d] got %+v want %+v\nops=%s",
					c, i, mReconConcat[i], nReconConcat[i], opsString(ops))
			}
		}
		if len(mRev) != len(nRev) {
			t.Fatalf("case %d: reverse result count %d != %d", c, len(mRev), len(nRev))
		}
		for i := range nRev {
			if mRev[i] != nRev[i] {
				t.Fatalf("case %d: reverse[%d] got %v want %v\nops=%s",
					c, i, mRev[i], nRev[i], opsString(ops))
			}
		}
		assertSameState(t, m, n, fmt.Sprintf("case %d", c))

		if c < 5 {
			t.Logf("---- 随机用例 %d 输入 ----\n%s", c, opsString(ops))
			t.Logf("---- 随机用例 %d Reconcile 输出 ----\n%s", c, matchesString(nReconConcat))
			t.Logf("---- 随机用例 %d 判定依据: 与朴素模拟逐项一致, 有效匹配=%d, 禁配=%d, 银行未达=%d, 账簿未达=%d ----",
				c, len(n.matchList()), len(n.forbiddenList()),
				len(n.unmatched(Bank)), len(n.unmatched(Book)))
		}
	}
	t.Logf("判定: %d 组随机序列的新增匹配(含 mid/轮次)、Reverse 结果、F、未达账项与朴素模拟完全一致", cases)
}

func generateOps(rng *rand.Rand) []fuzzOp {
	nLines := 2 + rng.Intn(14)
	refs := []string{"", "", "R1", "R2", "R3"}
	pickRef := func() string { return refs[rng.Intn(len(refs))] }

	bankIDs := make([]string, nLines)
	bookIDs := make([]string, nLines)
	for i := range bankIDs {
		bankIDs[i] = fmt.Sprintf("b%02d", i)
		bookIDs[i] = fmt.Sprintf("k%02d", i)
	}
	rng.Shuffle(len(bankIDs), func(i, j int) { bankIDs[i], bankIDs[j] = bankIDs[j], bankIDs[i] })
	rng.Shuffle(len(bookIDs), func(i, j int) { bookIDs[i], bookIDs[j] = bookIDs[j], bookIDs[i] })

	amtPool := []int64{10, 20, 30, 50, 100, 150, 200, 300, -10, -20, -50}
	pickAmt := func() int64 { return amtPool[rng.Intn(len(amtPool))] }

	var ops []fuzzOp
	addedBanks := 0
	addedBooks := 0
	for addedBanks < nLines || addedBooks < nLines {
		switch rng.Intn(2) {
		case 0:
			if addedBanks < nLines {
				ops = append(ops, fuzzOp{
					kind: opAddBank, id: bankIDs[addedBanks],
					amt: pickAmt(), day: int64(rng.Intn(40)), ref: pickRef(),
				})
				addedBanks++
			}
		case 1:
			if addedBooks < nLines {
				ops = append(ops, fuzzOp{
					kind: opAddBook, id: bookIDs[addedBooks],
					amt: pickAmt(), day: int64(rng.Intn(40)), ref: pickRef(),
				})
				addedBooks++
			}
		}
	}

	// 追加若干轮 Reconcile / Reverse 交错操作。
	maxMid := int64(0)
	rounds := 1 + rng.Intn(4)
	for r := 0; r < rounds; r++ {
		ops = append(ops, fuzzOp{kind: opReconcile, w: int64(rng.Intn(5))})
		// 估算 maxMid 以便 Reverse 采样（朴素结果稍后才知道，这里按宽松上界）。
		maxMid += int64(nLines)
		if rng.Intn(2) == 0 && maxMid > 0 {
			mid := int64(1 + rng.Intn(int(maxMid)+2))
			ops = append(ops, fuzzOp{kind: opReverse, mid: mid})
		}
	}
	// 偶尔再追加一批小金额行，制造撤销后新行匹配场景。
	if rng.Intn(2) == 0 {
		ops = append(ops, fuzzOp{kind: opAddBank, id: fmt.Sprintf("bx%02d", rng.Intn(99)),
			amt: pickAmt(), day: int64(rng.Intn(40)), ref: pickRef()})
		ops = append(ops, fuzzOp{kind: opAddBook, id: fmt.Sprintf("kx%02d", rng.Intn(99)),
			amt: pickAmt(), day: int64(rng.Intn(40)), ref: pickRef()})
		ops = append(ops, fuzzOp{kind: opReconcile, w: int64(rng.Intn(5))})
	}
	return ops
}

func opsString(ops []fuzzOp) string {
	var b strings.Builder
	for i, op := range ops {
		switch op.kind {
		case opAddBank:
			fmt.Fprintf(&b, "%d AddLine(Bank,%s,%d,%d,%q)\n", i, op.id, op.amt, op.day, op.ref)
		case opAddBook:
			fmt.Fprintf(&b, "%d AddLine(Book,%s,%d,%d,%q)\n", i, op.id, op.amt, op.day, op.ref)
		case opReconcile:
			fmt.Fprintf(&b, "%d Reconcile(W=%d)\n", i, op.w)
		case opReverse:
			fmt.Fprintf(&b, "%d Reverse(mid=%d)\n", i, op.mid)
		}
	}
	return b.String()
}

func matchesString(ms []Match) string {
	var b strings.Builder
	for _, mm := range ms {
		fmt.Fprintf(&b, "mid=%d round=%d bank=%v book=%v\n", mm.Mid, mm.Round, mm.BankID, mm.BookID)
	}
	if len(ms) == 0 {
		return "(无新增匹配)\n"
	}
	return b.String()
}
