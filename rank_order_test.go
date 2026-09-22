package ontology

import "testing"

// permutations generates k distinct deterministic permutations of rows
// using an LCG-seeded Fisher-Yates shuffle, so the test never depends on
// map iteration or a nondeterministic random seed.
func permutations(rows []Row, k int) [][]Row {
	n := len(rows)
	seen := make(map[string]bool)
	out := make([][]Row, 0, k)
	seed := uint64(0x9e3779b97f4a7c15)
	for len(out) < k {
		seed = seed*6364136223846793005 + 1442695040888963407
		state := seed
		perm := make([]Row, n)
		copy(perm, rows)
		for i := n - 1; i > 0; i-- {
			state = state*6364136223846793005 + 1442695040888963407
			j := int((state >> 33) % uint64(i+1))
			perm[i], perm[j] = perm[j], perm[i]
		}
		key := permKey(perm)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, perm)
	}
	return out
}

func permKey(rows []Row) string {
	b := make([]byte, 0, len(rows)*4)
	for _, r := range rows {
		b = append(b, r.ID...)
		b = append(b, '|')
	}
	return string(b)
}

func TestTieOrderStableAcrossPermutations(t *testing.T) {
	values := []float64{10, 20, 20, 30, 20, 10, 30}
	base := rowsFrom(values)
	want := Rank(base, Asc).Rows

	perms := permutations(base, 20)
	if len(perms) < 20 {
		t.Fatalf("need >=20 permutations, got %d", len(perms))
	}
	for pi, perm := range perms {
		got := Rank(perm, Asc).Rows
		if len(got) != len(want) {
			t.Fatalf("perm %d: %d rows, want %d", pi, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("perm %d row %d:\n got  %+v\n want %+v", pi, i, got[i], want[i])
			}
		}
	}
	// Ties must appear in ID-ascending order, not input order.
	if want[2].ID != "id001" || want[3].ID != "id002" || want[4].ID != "id004" {
		t.Errorf("tie group IDs = %s,%s,%s, want id001,id002,id004",
			want[2].ID, want[3].ID, want[4].ID)
	}
}

func TestDescNotReverseOfAsc(t *testing.T) {
	values := []float64{10, 20, 20, 30}
	asc := Rank(rowsFrom(values), Asc).Rows
	desc := Rank(rowsFrom(values), Desc).Rows

	wantDesc := []triple{{1, 1, 1}, {2, 2, 2}, {3, 2, 2}, {4, 4, 3}}
	gotDesc := triplesOf(t, Result{Rows: desc})
	for i := range wantDesc {
		if gotDesc[i] != wantDesc[i] {
			t.Errorf("desc row %d: got %+v, want %+v", i, gotDesc[i], wantDesc[i])
		}
	}

	// Desc values must run high to low: it is not the asc list flipped,
	// because inside ties IDs still ascend.
	if desc[0].SortValue != 30 || desc[3].SortValue != 10 {
		t.Fatalf("desc value order = %v,%v,%v,%v",
			desc[0].SortValue, desc[1].SortValue, desc[2].SortValue, desc[3].SortValue)
	}
	if desc[1].ID != "id001" || desc[2].ID != "id002" {
		t.Errorf("desc tie group IDs = %s,%s, want id001,id002 (ascending)",
			desc[1].ID, desc[2].ID)
	}
	// The desc ID sequence must not be the asc ID sequence reversed:
	// endpoints agree, but tie rows stay ID-ascending in the middle.
	descIDs := []string{desc[0].ID, desc[1].ID, desc[2].ID, desc[3].ID}
	revAscIDs := []string{
		asc[3].ID, asc[2].ID, asc[1].ID, asc[0].ID,
	}
	same := true
	for i := range descIDs {
		if descIDs[i] != revAscIDs[i] {
			same = false
		}
	}
	if same {
		t.Fatalf("desc IDs %v equal reversed asc IDs %v", descIDs, revAscIDs)
	}
}
