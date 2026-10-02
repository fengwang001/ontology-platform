package broadphase

import (
	"math/rand"
	"reflect"
	"testing"
)

type naiveObject struct {
	tight Box
	fat   Box
	layer int
	mask  int
}

type naiveState struct {
	margin  int64
	objects map[int64]naiveObject
}

func newNaiveState(margin int64) *naiveState {
	return &naiveState{margin: margin, objects: make(map[int64]naiveObject)}
}

func naivePair(a, b int64) Pair {
	if a > b {
		a, b = b, a
	}
	return Pair{A: a, B: b}
}

func naiveCanCollide(a, b naiveObject) bool {
	return a.layer&b.mask != 0 && b.layer&a.mask != 0
}

func naiveIntersects(a, b Box) bool {
	return a.LX < b.HX && b.LX < a.HX && a.LY < b.HY && b.LY < a.HY
}

func naiveSets(state *naiveState) (map[Pair]struct{}, map[Pair]struct{}) {
	fat := make(map[Pair]struct{})
	contact := make(map[Pair]struct{})
	ids := make([]int64, 0, len(state.objects))
	for id := range state.objects {
		ids = append(ids, id)
	}
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			a := state.objects[ids[i]]
			b := state.objects[ids[j]]
			pair := naivePair(ids[i], ids[j])
			if naiveCanCollide(a, b) && naiveIntersects(a.fat, b.fat) {
				fat[pair] = struct{}{}
				if naiveIntersects(a.tight, b.tight) {
					contact[pair] = struct{}{}
				}
			}
		}
	}
	return fat, contact
}

func naiveDiff(before, after map[Pair]struct{}) ([]Pair, []Pair) {
	var entered, exited []Pair
	for pair := range after {
		if _, ok := before[pair]; !ok {
			entered = append(entered, pair)
		}
	}
	for pair := range before {
		if _, ok := after[pair]; !ok {
			exited = append(exited, pair)
		}
	}
	sortPairsForTest(entered)
	sortPairsForTest(exited)
	return entered, exited
}

func sortPairsForTest(pairs []Pair) {
	for i := 0; i < len(pairs); i++ {
		for j := i + 1; j < len(pairs); j++ {
			if pairs[j].A < pairs[i].A || (pairs[j].A == pairs[i].A && pairs[j].B < pairs[i].B) {
				pairs[i], pairs[j] = pairs[j], pairs[i]
			}
		}
	}
}

func normalizeList(pairs []Pair) []Pair {
	if pairs == nil {
		return []Pair{}
	}
	return pairs
}

func assertSameEvents(t *testing.T, got, want Events, op string) {
	t.Helper()
	got = Events{
		FatEnter:     normalizeList(got.FatEnter),
		FatExit:      normalizeList(got.FatExit),
		ContactEnter: normalizeList(got.ContactEnter),
		ContactExit:  normalizeList(got.ContactExit),
	}
	want = Events{
		FatEnter:     normalizeList(want.FatEnter),
		FatExit:      normalizeList(want.FatExit),
		ContactEnter: normalizeList(want.ContactEnter),
		ContactExit:  normalizeList(want.ContactExit),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s events got=%+v want=%+v", op, got, want)
	}
}

func randomValidBox(random *rand.Rand) Box {
	lx := int64(random.Intn(21) - 10)
	ly := int64(random.Intn(21) - 10)
	hx := lx + int64(random.Intn(5)+1)
	hy := ly + int64(random.Intn(5)+1)
	return Box{LX: lx, HX: hx, LY: ly, HY: hy}
}

func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	const operationsPerSequence = 24
	random := rand.New(rand.NewSource(73491))

	for sequence := 0; sequence < sequences; sequence++ {
		bp, err := New(2, 60)
		if err != nil {
			t.Fatal(err)
		}
		naive := newNaiveState(2)

		for step := 0; step < operationsPerSequence; step++ {
			action := random.Intn(100)
			switch {
			case action < 25:
				id := int64(random.Intn(70) + 1)
				box := randomValidBox(random)
				layer := random.Intn(4)
				mask := random.Intn(4)
				t.Logf("seq=%d step=%d Insert(%d,%+v,%d,%d)", sequence, step, id, box, layer, mask)
				_, existed := naive.objects[id]
				got, gerr := bp.Insert(id, box, layer, mask)
				if existed {
					if gerr != ErrObjectExists {
						t.Fatalf("Insert existing got err %v want ErrObjectExists", gerr)
					}
					break
				}
				if len(naive.objects) == 60 {
					if gerr != ErrCapacityReached {
						t.Fatalf("Insert full got err %v want ErrCapacityReached", gerr)
					}
					break
				}
				if gerr != nil {
					t.Fatalf("Insert got unexpected err %v", gerr)
				}
				beforeFat, beforeContact := naiveSets(naive)
				naive.objects[id] = naiveObject{
					tight: box,
					fat:   expandBox(box, naive.margin),
					layer: layer,
					mask:  mask,
				}
				afterFat, afterContact := naiveSets(naive)
				fatEnter, fatExit := naiveDiff(beforeFat, afterFat)
				contactEnter, contactExit := naiveDiff(beforeContact, afterContact)
				want := Events{FatEnter: fatEnter, FatExit: fatExit, ContactEnter: contactEnter, ContactExit: contactExit}
				t.Logf("Insert output got=%+v basis=%+v", got, want)
				assertSameEvents(t, got, want, "Insert")

			case action < 55:
				if len(naive.objects) == 0 {
					break
				}
				var id int64
				for candidate := range naive.objects {
					id = candidate
					break
				}
				box := randomValidBox(random)
				t.Logf("seq=%d step=%d Move(%d,%+v)", sequence, step, id, box)
				old := naive.objects[id]
				wantRefat := !boxContains(old.fat, box)
				got, gerr := bp.Move(id, box)
				if gerr != nil {
					t.Fatalf("Move got unexpected err %v", gerr)
				}
				if got.Refatted != wantRefat {
					t.Fatalf("Move refat got=%v want=%v", got.Refatted, wantRefat)
				}
				if !wantRefat && (bp.crossed != 0 || bp.checks != 0) {
					t.Fatalf("non-refat counters crossed=%d checks=%d", bp.crossed, bp.checks)
				}
				if bp.checks > bp.crossed {
					t.Fatalf("pairChecks %d > crossed %d", bp.checks, bp.crossed)
				}
				beforeFat, beforeContact := naiveSets(naive)
				if wantRefat {
					old.fat = expandBox(box, naive.margin)
				}
				old.tight = box
				naive.objects[id] = old
				afterFat, afterContact := naiveSets(naive)
				fatEnter, fatExit := naiveDiff(beforeFat, afterFat)
				contactEnter, contactExit := naiveDiff(beforeContact, afterContact)
				want := Events{FatEnter: fatEnter, FatExit: fatExit, ContactEnter: contactEnter, ContactExit: contactExit}
				t.Logf("Move output got=%+v refatted=%v basis=%+v crossed=%d checks=%d", got.Events, got.Refatted, want, bp.crossed, bp.checks)
				assertSameEvents(t, got.Events, want, "Move")

			case action < 70:
				if len(naive.objects) == 0 {
					break
				}
				var id int64
				for candidate := range naive.objects {
					id = candidate
					break
				}
				t.Logf("seq=%d step=%d Remove(%d)", sequence, step, id)
				got, gerr := bp.Remove(id)
				if gerr != nil {
					t.Fatalf("Remove got unexpected err %v", gerr)
				}
				beforeFat, beforeContact := naiveSets(naive)
				delete(naive.objects, id)
				afterFat, afterContact := naiveSets(naive)
				fatEnter, fatExit := naiveDiff(beforeFat, afterFat)
				contactEnter, contactExit := naiveDiff(beforeContact, afterContact)
				want := Events{FatEnter: fatEnter, FatExit: fatExit, ContactEnter: contactEnter, ContactExit: contactExit}
				t.Logf("Remove output got=%+v basis=%+v", got, want)
				assertSameEvents(t, got, want, "Remove")

			case action < 85:
				if len(naive.objects) == 0 {
					break
				}
				var id int64
				for candidate := range naive.objects {
					id = candidate
					break
				}
				layer := random.Intn(4)
				mask := random.Intn(4)
				t.Logf("seq=%d step=%d SetFilter(%d,%d,%d)", sequence, step, id, layer, mask)
				got, gerr := bp.SetFilter(id, layer, mask)
				if gerr != nil {
					t.Fatalf("SetFilter got unexpected err %v", gerr)
				}
				beforeFat, beforeContact := naiveSets(naive)
				updated := naive.objects[id]
				updated.layer = layer
				updated.mask = mask
				naive.objects[id] = updated
				afterFat, afterContact := naiveSets(naive)
				fatEnter, fatExit := naiveDiff(beforeFat, afterFat)
				contactEnter, contactExit := naiveDiff(beforeContact, afterContact)
				want := Events{FatEnter: fatEnter, FatExit: fatExit, ContactEnter: contactEnter, ContactExit: contactExit}
				t.Logf("SetFilter output got=%+v basis=%+v", got, want)
				assertSameEvents(t, got, want, "SetFilter")

			default:
				switch random.Intn(4) {
				case 0:
					if _, err := New(-1, 1); err != ErrInvalidArgument {
						t.Fatalf("invalid New err=%v", err)
					}
				case 1:
					if _, err := bp.Insert(0, box(0, 1, 0, 1)); err != ErrInvalidArgument {
						t.Fatalf("invalid Insert err=%v", err)
					}
				case 2:
					if _, err := bp.Move(999, box(0, 1, 0, 1)); err != ErrObjectNotFound {
						t.Fatalf("missing Move err=%v", err)
					}
				default:
					if _, err := bp.SetFilter(999, 0, 1); err != ErrObjectNotFound {
						t.Fatalf("missing SetFilter err=%v", err)
					}
				}
			}

			gotFat := make(map[Pair]struct{})
			for _, pair := range bp.Pairs() {
				gotFat[pair] = struct{}{}
			}
			gotContact := make(map[Pair]struct{})
			for _, pair := range bp.Contacts() {
				gotContact[pair] = struct{}{}
			}
			wantFat, wantContact := naiveSets(naive)
			if !reflect.DeepEqual(gotFat, wantFat) || !reflect.DeepEqual(gotContact, wantContact) {
				t.Fatalf("seq=%d step=%d sets mismatch fat got=%v want=%v contact got=%v want=%v",
					sequence, step, gotFat, wantFat, gotContact, wantContact)
			}
		}
	}
}
