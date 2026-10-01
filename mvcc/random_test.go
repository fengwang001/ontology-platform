package mvcc

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestRandomDifferentialAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		s := NewStore()
		n := newNaive()
		snapCount := 0
		ops := 20 + rng.Intn(30)
		for i := 0; i < ops; i++ {
			txID := func() int {
				if rng.Intn(20) == 0 {
					return -rng.Intn(3)
				}
				return 1 + rng.Intn(10)
			}
			tuple := func() string {
				if rng.Intn(15) == 0 {
					return "ghost"
				}
				return fmt.Sprintf("t%d", rng.Intn(8))
			}
			cmd := func() int { return rng.Intn(10) - 1 }
			snapID := func() int { return 1 + rng.Intn(snapCount+1) }

			var desc string
			var errStore, errNaive error
			switch rng.Intn(100) {
			case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11:
				x := txID()
				desc = fmt.Sprintf("Begin(%d)", x)
				errStore, errNaive = s.Begin(x), n.begin(x)
			case 12, 13, 14, 15, 16, 17, 18, 19, 20, 21:
				x, p := txID(), txID()
				desc = fmt.Sprintf("BeginSub(%d,%d)", x, p)
				errStore, errNaive = s.BeginSub(x, p), n.beginSub(x, p)
			case 22, 23, 24, 25, 26, 27:
				x := txID()
				desc = fmt.Sprintf("CommitSub(%d)", x)
				errStore, errNaive = s.CommitSub(x), n.commitSub(x)
			case 28, 29, 30, 31, 32, 33:
				x := txID()
				desc = fmt.Sprintf("Commit(%d)", x)
				errStore, errNaive = s.Commit(x), n.commit(x)
			case 34, 35, 36, 37, 38, 39:
				x := txID()
				desc = fmt.Sprintf("Abort(%d)", x)
				errStore, errNaive = s.Abort(x), n.abort(x)
			case 40, 41, 42, 43, 44, 45, 46, 47:
				idStore, idNaive := s.Snapshot(), n.snapshot()
				snapCount++
				desc = "Snapshot()"
				if idStore != idNaive {
					t.Fatalf("seq=%d op=%d %s: snapshot id %d != %d", seq, i, desc, idStore, idNaive)
				}
				desc = fmt.Sprintf("Snapshot() -> %d", idStore)
			case 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63:
				tup, x, c := tuple(), txID(), cmd()
				desc = fmt.Sprintf("Insert(%s,%d,%d)", tup, x, c)
				errStore, errNaive = s.Insert(tup, x, c), n.insert(tup, x, c)
			case 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 77:
				tup, x, c := tuple(), txID(), cmd()
				desc = fmt.Sprintf("Delete(%s,%d,%d)", tup, x, c)
				errStore, errNaive = s.Delete(tup, x, c), n.delete(tup, x, c)
			default:
				tup, x, c, sp := tuple(), txID(), cmd(), snapID()
				desc = fmt.Sprintf("Visible(%s,%d,%d,%d)", tup, x, c, sp)
				visStore, whyStore, errS := s.Explain(tup, x, c, sp)
				visNaive, whyNaive, errN := n.visible(tup, x, c, sp)
				errStore, errNaive = errS, errN
				if errStore == nil && errNaive == nil {
					if visStore != visNaive {
						t.Fatalf("seq=%d op=%d %s: store=%v naive=%v\nstore: %s\nnaive: %s",
							seq, i, desc, visStore, visNaive, whyStore, whyNaive)
					}
					desc = fmt.Sprintf("%s -> %v | %s", desc, visStore, whyStore)
				}
			}
			if errStore != errNaive {
				t.Fatalf("seq=%d op=%d %s: store err=%v, naive err=%v", seq, i, desc, errStore, errNaive)
			}
			t.Logf("seq=%d op=%02d %s err=%v", seq, i, desc, errStore)
		}
	}
}
