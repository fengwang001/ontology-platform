package hotrank

import "testing"

func TestSpecExample(t *testing.T) {
	b, err := New(10, 3, 100, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	must := func(e error) {
		if e != nil {
			t.Fatal(e)
		}
	}
	must(b.Add("a", 60, 0))
	must(b.Add("b", 100, 5))
	must(b.Add("a", 50, 12))

	r, err := b.Snapshot(15)
	must(err)
	if len(r.Board) != 2 || r.Board[0].ID != "a" || r.Board[0].Score != 110 || r.Board[0].Rank != 1 ||
		!r.Board[0].New || r.Board[1].ID != "b" || r.Board[1].Score != 100 || r.Board[1].Rank != 2 {
		t.Fatalf("snapshot1 %+v", r.Board)
	}

	must(b.Add("c", 100, 25))
	r, err = b.Snapshot(25)
	must(err)
	if len(r.Board) != 3 {
		t.Fatalf("snapshot2 %+v", r.Board)
	}
	want := []RankItem{
		{ID: "a", Score: 110, Rank: 1, Change: 0},
		{ID: "b", Score: 100, Rank: 2, Change: 0},
		{ID: "c", Score: 100, Rank: 2, New: true},
	}
	for i := range want {
		if r.Board[i] != want[i] {
			t.Fatalf("snapshot2[%d] got %+v want %+v", i, r.Board[i], want[i])
		}
	}

	r, err = b.Snapshot(35)
	must(err)
	if len(r.Board) != 1 || r.Board[0] != (RankItem{ID: "c", Score: 100, Rank: 1, Change: 1}) {
		t.Fatalf("snapshot3 board %+v", r.Board)
	}
	if len(r.Dropped) != 2 || r.Dropped[0] != (DroppedItem{ID: "a", PreviousRank: 1}) ||
		r.Dropped[1] != (DroppedItem{ID: "b", PreviousRank: 2}) {
		t.Fatalf("snapshot3 dropped %+v", r.Dropped)
	}

	must(b.Add("a", 30, 36))
	r, err = b.Snapshot(36)
	must(err)
	if len(r.Board) != 1 || r.Board[0].ID != "c" || r.Board[0].Change != 0 {
		t.Fatalf("snapshot4 %+v", r.Board)
	}

	must(b.Add("c", 80, 52))
	r, err = b.Snapshot(52)
	must(err)
	if len(r.Board) != 1 || r.Board[0] != (RankItem{ID: "c", Score: 80, Rank: 1, Change: 0}) {
		t.Fatalf("snapshot5 %+v", r.Board)
	}
	if b.hc["c"] != 1 {
		t.Fatalf("hc = %d", b.hc["c"])
	}

	r, err = b.Snapshot(53)
	must(err)
	if len(r.Board) != 0 || len(r.Dropped) != 1 || r.Dropped[0] != (DroppedItem{ID: "c", PreviousRank: 1}) {
		t.Fatalf("snapshot6 board=%+v dropped=%+v", r.Board, r.Dropped)
	}
}
