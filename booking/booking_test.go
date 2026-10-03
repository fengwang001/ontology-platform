package booking

import (
	"errors"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, c int, s []int64, rho int64) *Book {
	t.Helper()
	b, err := New(c, s, rho)
	if err != nil {
		t.Fatalf("New(%d, %v, %d) = %v", c, s, rho, err)
	}
	return b
}

func bookedIDs(b *Book) []string {
	cs := b.BookedContracts()
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func waitIDs(b *Book) []string {
	cs := b.WaitlistContracts()
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func assertState(t *testing.T, b *Book, wantBooked, wantWait []string) {
	t.Helper()
	if got := bookedIDs(b); !reflect.DeepEqual(got, wantBooked) {
		t.Fatalf("booked = %v, want %v", got, wantBooked)
	}
	if got := waitIDs(b); !reflect.DeepEqual(got, wantWait) {
		t.Fatalf("waitlist = %v, want %v", got, wantWait)
	}
}

func assertErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("err = %v, want %v", got, want)
	}
}

func TestNewValidation(t *testing.T) {
	bad := []struct {
		c   int
		s   []int64
		rho int64
	}{
		{0, []int64{}, 100},
		{11, make([]int64, 11), 100},
		{2, []int64{1}, 100},
		{2, []int64{1, -1}, 100},
		{2, []int64{1, 1e12 + 1}, 100},
		{2, []int64{1, 1}, 0},
		{2, []int64{1, 1}, 1001},
	}
	for _, tc := range bad {
		if _, err := New(tc.c, tc.s, tc.rho); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("New(%d, %v, %d) = %v, want ErrInvalidParam", tc.c, tc.s, tc.rho, err)
		}
	}
	if _, err := New(1, []int64{0}, 1); err != nil {
		t.Fatalf("boundary min: %v", err)
	}
	if _, err := New(10, []int64{1e12, 0, 1, 2, 3, 4, 5, 6, 7, 8}, 1000); err != nil {
		t.Fatalf("boundary max: %v", err)
	}
}

// The worked example from the specification.
func TestSpecExample(t *testing.T) {
	b := mustNew(t, 3, []int64{100, 50, 80}, 90)
	assertErr(t, b.Book("a", 0b001, 80), nil)
	assertState(t, b, []string{"a"}, []string{})

	assertErr(t, b.Book("b", 0b011, 60), nil) // feasible alone, joins waitlist
	assertState(t, b, []string{"a"}, []string{"b"})

	assertErr(t, b.Book("c", 0b010, 40), nil)
	assertState(t, b, []string{"a", "c"}, []string{"b"})

	assertErr(t, b.Cancel("a"), nil) // promotes b
	assertState(t, b, []string{"b", "c"}, []string{})
}

// qty == Cap(mask) is bookable; qty == Cap(mask)+1 is never bookable and
// does not enter the waitlist.
func TestQtyExactlyCap(t *testing.T) {
	b := mustNew(t, 2, []int64{100, 50}, 90) // Cap({0})=90, Cap({1})=45, Cap({0,1})=135
	assertErr(t, b.Book("x", 0b01, 90), nil)
	assertState(t, b, []string{"x"}, []string{})

	assertErr(t, b.Book("y", 0b10, 46), ErrNeverBookable)
	assertState(t, b, []string{"x"}, []string{})

	assertErr(t, b.Book("z", 0b10, 45), nil)
	assertState(t, b, []string{"x", "z"}, []string{})
}

// Subset sum exactly Cap(T) is feasible; one more unit is not.
func TestSubsetSumExactlyCap(t *testing.T) {
	b := mustNew(t, 2, []int64{100, 100}, 100) // Cap({0,1})=200
	assertErr(t, b.Book("a", 0b01, 100), nil)
	assertErr(t, b.Book("b", 0b10, 100), nil) // T={0,1} sum = 200 = Cap
	assertState(t, b, []string{"a", "b"}, []string{})

	assertErr(t, b.Book("c", 0b01, 1), nil) // alone fine, but T={0,1} would be 201
	assertState(t, b, []string{"a", "b"}, []string{"c"})

	assertErr(t, b.Resize("a", 101), ErrInfeasible) // grow rejected, no waitlist
	assertState(t, b, []string{"a", "b"}, []string{"c"})
}

// Capacity floors the whole subset sum, not the per-unit sum: [105,55],
// rho=90 gives Cap({0,1}) = floor(90*160/100) = 144, while per-unit floors
// would give 94+49 = 143.
func TestOverallRounding(t *testing.T) {
	b := mustNew(t, 2, []int64{105, 55}, 90)
	assertErr(t, b.Book("x", 0b11, 144), nil) // bookable only with overall rounding
	assertState(t, b, []string{"x"}, []string{})

	assertErr(t, b.Book("y", 0b01, 1), nil) // T={0,1}: 144+1 > 144
	assertState(t, b, []string{"x"}, []string{"y"})

	assertErr(t, b.Book("z", 0b11, 145), ErrNeverBookable)
	assertState(t, b, []string{"x"}, []string{"y"})
}

// Cross-unit targeting: each single cell looks fine, but the union subset
// is infeasible.
func TestCrossSubsetInfeasible(t *testing.T) {
	b := mustNew(t, 2, []int64{100, 100}, 100)
	assertErr(t, b.Book("a", 0b01, 100), nil)
	assertErr(t, b.Book("b", 0b10, 100), nil)
	assertErr(t, b.Book("c", 0b11, 1), nil) // Cap({0,1})=200, load would be 201
	assertState(t, b, []string{"a", "b"}, []string{"c"})

	assertErr(t, b.Cancel("b"), nil) // frees 100 on T={0,1}: c fits now
	assertState(t, b, []string{"a", "c"}, []string{})
}

// A stuck head of the waitlist does not block later entries.
func TestWaitlistHeadDoesNotBlock(t *testing.T) {
	b := mustNew(t, 2, []int64{100, 100}, 100)
	assertErr(t, b.Book("a", 0b01, 100), nil)
	assertErr(t, b.Book("b", 0b10, 100), nil)
	assertErr(t, b.Book("big", 0b11, 150), nil)   // needs 150 free on the universe
	assertErr(t, b.Book("small", 0b10, 100), nil) // needs 100 free on {1}
	assertState(t, b, []string{"a", "b"}, []string{"big", "small"})

	assertErr(t, b.Cancel("b"), nil) // frees 100: big still stuck, small fits
	assertState(t, b, []string{"a", "small"}, []string{"big"})
}

// Promotion is FIFO and contracts promoted earlier in the same scan consume
// capacity visible to later entries of that scan.
func TestPromotionOrderAndInRoundEffects(t *testing.T) {
	b := mustNew(t, 2, []int64{100, 100}, 100) // Cap({0,1})=200
	assertErr(t, b.Book("a", 0b01, 200), ErrNeverBookable)
	assertErr(t, b.Book("a", 0b11, 200), nil)
	assertErr(t, b.Book("w1", 0b01, 100), nil)
	assertErr(t, b.Book("w2", 0b10, 100), nil)
	assertErr(t, b.Book("w3", 0b11, 1), nil)
	assertState(t, b, []string{"a"}, []string{"w1", "w2", "w3"})

	// Freeing 200 promotes w1 then w2 in one scan; w1+w2 = 200 fills
	// T={0,1}, so w3 stays even though it only needs 1.
	assertErr(t, b.Cancel("a"), nil)
	assertState(t, b, []string{"w1", "w2"}, []string{"w3"})
}

func TestResizeShrinkPromotesAndGrowRejected(t *testing.T) {
	b := mustNew(t, 2, []int64{100, 100}, 100)
	assertErr(t, b.Book("a", 0b11, 200), nil)
	assertErr(t, b.Book("w", 0b01, 50), nil)
	assertState(t, b, []string{"a"}, []string{"w"})

	assertErr(t, b.Resize("a", 150), nil) // shrink triggers promotion
	assertState(t, b, []string{"a", "w"}, []string{})

	assertErr(t, b.Resize("a", 151), ErrInfeasible) // grow must stay feasible
	assertState(t, b, []string{"a", "w"}, []string{})

	checks := b.SubsetChecks()
	assertErr(t, b.Resize("a", 150), nil) // equal is a no-op, no subset checks
	if got := b.SubsetChecks(); got != checks {
		t.Fatalf("no-op resize performed %d subset checks", got-checks)
	}
	assertState(t, b, []string{"a", "w"}, []string{})
}

func TestRetargetExpandShrinkAndNoop(t *testing.T) {
	b := mustNew(t, 3, []int64{100, 50, 80}, 90)
	assertErr(t, b.Book("a", 0b001, 80), nil)
	assertErr(t, b.Book("w", 0b011, 60), nil) // waitlisted: T={0,1} 80+60>135
	assertState(t, b, []string{"a"}, []string{"w"})

	// Expand a's target to {0,2}: its load leaves T={0,1}, promoting w.
	assertErr(t, b.Retarget("a", 0b101), nil)
	assertState(t, b, []string{"a", "w"}, []string{})

	// Expand w to the full set: only the universe subset is examined.
	assertErr(t, b.Retarget("w", 0b111), nil)
	assertState(t, b, []string{"a", "w"}, []string{})

	checks := b.SubsetChecks()
	assertErr(t, b.Retarget("w", 0b111), nil) // same mask: no-op, no promotion
	if got := b.SubsetChecks(); got != checks {
		t.Fatalf("no-op retarget performed %d subset checks", got-checks)
	}

	// Shrink w's target back to {0,1}: load joins T={0,1} (cap 135, free).
	assertErr(t, b.Retarget("w", 0b011), nil)
	assertState(t, b, []string{"a", "w"}, []string{})
}

func TestRetargetInfeasible(t *testing.T) {
	b := mustNew(t, 2, []int64{100, 100}, 100)
	assertErr(t, b.Book("a", 0b01, 100), nil) // fills {0} to Cap
	assertErr(t, b.Book("c", 0b10, 50), nil)

	before := b.SubsetChecks()
	assertErr(t, b.Retarget("c", 0b01), ErrInfeasible) // {0}: 100+50 > 100
	assertState(t, b, []string{"a", "c"}, []string{})
	if got := b.SubsetChecks(); got != before+1 {
		// Only T={0} is examined: {0,1} also contains the old mask {1}.
		t.Fatalf("retarget examined %d subsets, want 1", got-before)
	}
}

// Cancelling a waitlisted contract removes it without triggering promotion.
func TestCancelWaitlistedNoPromotion(t *testing.T) {
	b := mustNew(t, 2, []int64{100, 100}, 100)
	assertErr(t, b.Book("a", 0b11, 150), nil)
	assertErr(t, b.Book("w1", 0b01, 100), nil)
	assertErr(t, b.Book("w2", 0b10, 100), nil)
	assertState(t, b, []string{"a"}, []string{"w1", "w2"})

	checks := b.SubsetChecks()
	assertErr(t, b.Cancel("w1"), nil)
	if got := b.SubsetChecks(); got != checks {
		t.Fatalf("cancelling waitlisted contract triggered %d subset checks", got-checks)
	}
	assertState(t, b, []string{"a"}, []string{"w2"})
}

// Rejection reasons are reported in a fixed priority and rejected operations
// never change state.
func TestRejectionPriorities(t *testing.T) {
	b := mustNew(t, 2, []int64{100, 100}, 100)
	assertErr(t, b.Book("a", 0b01, 50), nil)
	assertErr(t, b.Book("d", 0b10, 100), nil)
	assertErr(t, b.Book("w", 0b11, 51), nil) // universe would be 150+51>200
	wantBooked, wantWait := bookedIDs(b), waitIDs(b)

	// Invalid params beat every other reason.
	assertErr(t, b.Book("", 0b01, 1), ErrInvalidParam)
	assertErr(t, b.Book("a", 0, 1), ErrInvalidParam)    // bad mask, dup id
	assertErr(t, b.Book("a", 0b01, 0), ErrInvalidParam) // bad qty, dup id
	assertErr(t, b.Book("a", 0b01, 1e12+1), ErrInvalidParam)
	assertErr(t, b.Book("x", 0b100, 1), ErrInvalidParam) // mask >= 2^C
	assertErr(t, b.Cancel(""), ErrInvalidParam)
	assertErr(t, b.Resize("", 1), ErrInvalidParam)
	assertErr(t, b.Resize("missing", 0), ErrInvalidParam) // bad qty, missing id
	assertErr(t, b.Retarget("", 0b01), ErrInvalidParam)
	assertErr(t, b.Retarget("missing", 0), ErrInvalidParam)

	// Duplicate id beats never-bookable.
	assertErr(t, b.Book("a", 0b01, 1e12), ErrDuplicateID)
	assertErr(t, b.Book("w", 0b01, 1e12), ErrDuplicateID) // dup even in waitlist

	// Not-found for Cancel/Resize/Retarget.
	assertErr(t, b.Cancel("missing"), ErrNotFound)
	assertErr(t, b.Resize("missing", 1), ErrNotFound)
	assertErr(t, b.Retarget("missing", 0b01), ErrNotFound)

	// Waitlisted contracts are not booked for Resize/Retarget.
	assertErr(t, b.Resize("w", 10), ErrNotBooked)
	assertErr(t, b.Retarget("w", 0b01), ErrNotBooked)

	// Never-bookable only after the above.
	assertErr(t, b.Book("nb", 0b01, 101), ErrNeverBookable)

	// Infeasible for Resize-grow and Retarget.
	assertErr(t, b.Resize("a", 101), ErrInfeasible)    // {0}: 101 > 100
	assertErr(t, b.Retarget("a", 0b10), ErrInfeasible) // {1}: 100+50 > 100

	// None of the rejections above changed booked set or waitlist.
	if got := bookedIDs(b); !reflect.DeepEqual(got, wantBooked) {
		t.Fatalf("rejected ops changed booked set: %v -> %v", wantBooked, got)
	}
	if got := waitIDs(b); !reflect.DeepEqual(got, wantWait) {
		t.Fatalf("rejected ops changed waitlist: %v -> %v", wantWait, got)
	}
	assertState(t, b, []string{"a", "d"}, []string{"w"})
}

// Subset examination counts follow the closed-form formulas, with no early
// exit, for C=4 and C=10.
func TestSubsetCheckCounts(t *testing.T) {
	for _, c := range []int{4, 10} {
		s := make([]int64, c)
		for i := range s {
			s[i] = 100
		}
		b := mustNew(t, c, s, 100)
		full := 1 << c

		// Book with mask of popcount k examines 2^(C-k) subsets.
		for _, mask := range []int{1, 0b101, full - 1} {
			k := bitsOnes(mask)
			before := b.SubsetChecks()
			assertErr(t, b.Book(idOf(mask), mask, 1), nil)
			want := int64(1 << (c - k))
			if got := b.SubsetChecks() - before; got != want {
				t.Fatalf("C=%d Book mask=%b: %d checks, want %d", c, mask, got, want)
			}
		}

		// The never-bookable single-point check is not counted.
		before := b.SubsetChecks()
		assertErr(t, b.Book("nb", 0b1, 101), ErrNeverBookable)
		if got := b.SubsetChecks(); got != before {
			t.Fatalf("C=%d: never-bookable check counted %d subsets", c, got-before)
		}

		// Retarget from m1 to m2 examines 2^(C-|m2|) - 2^(C-|m1|m2|) subsets.
		m1, m2 := 0b0001, 0b0110
		if c == 4 {
			m1, m2 = 0b0001, 0b0110
		} else {
			m1, m2 = 0b0000000001, 0b0000000110
		}
		assertErr(t, b.Book("rt", m1, 1), nil)
		before = b.SubsetChecks()
		assertErr(t, b.Retarget("rt", m2), nil)
		union := bitsOnes(m1 | m2)
		want := int64(1<<(c-bitsOnes(m2))) - int64(1<<(c-union))
		if got := b.SubsetChecks() - before; got != want {
			t.Fatalf("C=%d Retarget: %d checks, want %d", c, got, want)
		}
	}
}

// Feasibility checks enumerate every affected subset even when an early one
// already fails.
func TestNoEarlyExit(t *testing.T) {
	b := mustNew(t, 2, []int64{100, 100}, 100)
	assertErr(t, b.Book("a", 0b01, 100), nil)
	before := b.SubsetChecks()
	assertErr(t, b.Book("w", 0b01, 1), nil) // fails on T={0}, still scans T={0,1}
	if got := b.SubsetChecks() - before; got != 2 {
		t.Fatalf("examined %d subsets, want 2 (no early exit)", got)
	}
	assertState(t, b, []string{"a"}, []string{"w"})
}

func bitsOnes(m int) int {
	n := 0
	for m != 0 {
		n += m & 1
		m >>= 1
	}
	return n
}

func idOf(mask int) string {
	return "m" + string(rune('a'+mask%26)) + string(rune('0'+mask/26%10))
}
