package rotation

import (
	"sync"
	"testing"
)

// TestConcurrentSafety hammers one scheduler from many goroutines. With the
// single serialization lock every result corresponds to some serial order;
// after the run every accrued value must remain a slot-length multiple and
// the fairness spread must stay within one slot length.
func TestConcurrentSafety(t *testing.T) {
	s := New(nil)
	if err := s.AddRegion("R", 5); err != nil {
		t.Fatal(err)
	}
	for g := 1; g <= 4; g++ {
		_ = s.AddGroup("R", g)
	}
	for u := 0; u < 40; u++ {
		_ = s.AddUser(string(rune('a'+u)), "R", 1+u%4, Normal, 0)
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for k := 0; k < 60; k++ {
				switch k % 5 {
				case 0:
					_, _ = s.Issue("R", 1+id%4, 5, 30)
				case 1:
					_ = s.Advance(int64(5 * (k + 1)))
				case 2:
					_ = s.Confirm(string(rune('a'+id%40)), "R")
				case 3:
					_, _ = s.Accrued("R", 1)
				case 4:
					_ = s.MoveUser(string(rune('a'+id%40)), "R", 1+(id+1)%4)
				}
			}
		}(w)
	}
	wg.Wait()
	var mn, mx int64
	first := true
	for g := 1; g <= 4; g++ {
		a, err := s.Accrued("R", g)
		if err != nil {
			t.Fatal(err)
		}
		if a%5 != 0 {
			t.Fatalf("group %d accrued %d not a multiple of slotLen", g, a)
		}
		if first || a < mn {
			mn, first = a, false
		}
		if a > mx {
			mx = a
		}
	}
	if mx-mn > 5 {
		t.Fatalf("post-concurrency fairness spread %d > slotLen 5", mx-mn)
	}
}

// TestPerformanceShape verifies the stated complexity contracts:
//   - selecting groups depends on group count, not total user count;
//   - building notices depends on members of selected groups only;
//   - effective-level resolution does not scan ended/canceled orders.
func TestPerformanceShape(t *testing.T) {
	measure := func(usersPerGroup int) (selectNs float64, noticeNs float64) {
		s := New(nil)
		_ = s.AddRegion("R", 10)
		const groups = 16
		for g := 1; g <= groups; g++ {
			_ = s.AddGroup("R", g)
			for u := 0; u < usersPerGroup; u++ {
				_ = s.AddUser(groupUser(g, u), "R", g, Normal, 0)
			}
		}
		st := s.regions["R"]
		// Pure selection: no user traversal allowed.
		b0 := testing.AllocsPerRun(200, func() { sink = pickGroups(st, 4) })
		_ = b0
		// Notice generation touches exactly 4 groups' members.
		dest := map[string]*slotNotice{}
		m0 := testing.AllocsPerRun(50, func() {
			for k := range dest {
				delete(dest, k)
			}
			st.generateNotices([]int{1, 2, 3, 4}, dest)
		})
		return float64(b0), float64(m0)
	}
	_, small := measure(2)
	_, big := measure(400)
	// Notice allocations grow proportionally to selected-group membership
	// (4 groups): big/small ~ 200x; we only require it clearly grows while
	// selection (first value) stays flat - checked below directly.
	if big <= small*2 {
		t.Fatalf("notice work should scale with selected members, small=%v big=%v", small, big)
	}

	// Selection cost independence from user count: compare pickGroups output
	// identity with 0 vs many users via allocations, which must be identical.
	s := New(nil)
	_ = s.AddRegion("R", 10)
	for g := 1; g <= 8; g++ {
		_ = s.AddGroup("R", g)
	}
	st := s.regions["R"]
	a0 := testing.AllocsPerRun(300, func() { sink = pickGroups(st, 3) })
	for g := 1; g <= 8; g++ {
		for u := 0; u < 500; u++ {
			_ = s.AddUser(groupUser(g, u), "R", g, Normal, 0)
		}
	}
	a1 := testing.AllocsPerRun(300, func() { sink = pickGroups(st, 3) })
	if a1 > a0+1 {
		t.Fatalf("pickGroups allocations grew with user count: %v -> %v", a0, a1)
	}

	// Effective level must not retain ended/canceled orders in the active set.
	id1, _ := s.Issue("R", 1, 10, 20)
	_ = s.Advance(30)
	if len(st.book.active) != 0 {
		t.Fatalf("ended order %d still in active set: %v", id1, st.book.sortedActiveIDs())
	}
	id2, _ := s.Issue("R", 1, 40, 60)
	_ = s.Cancel(id2) // before start -> removed entirely
	if _, exists := st.book.byID[id2]; exists {
		t.Fatalf("canceled-before-start order %d must be as never-existed", id2)
	}
}

var sink []int

func groupUser(g, u int) string {
	return string(rune('A'+(g-1))) + "-" + itoa(u)
}

func itoa(x int) string {
	if x == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for x > 0 {
		i--
		b[i] = byte('0' + x%10)
		x /= 10
	}
	return string(b[i:])
}
