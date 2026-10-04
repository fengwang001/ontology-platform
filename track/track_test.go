package track

import (
	"errors"
	"sync"
	"testing"

	"ontology/cue"
	"ontology/edit"
)

var exampleList = edit.EditList{
	Deletions:  []edit.Deletion{{A: 2000, B: 5000}},
	Insertions: []edit.Insertion{{At: 8000, Len: 1000}},
}

func fresh(t *testing.T, id string) {
	t.Helper()
	resetTracks()
	if err := CreateTrack(id, 500); err != nil {
		t.Fatal(err)
	}
}

func getMust(t *testing.T, id string, v int) []cue.Cue {
	t.Helper()
	cs, err := Get(id, v)
	if err != nil {
		t.Fatalf("Get(%d): %v", v, err)
	}
	return cs
}

func TestBasicVersions(t *testing.T) {
	fresh(t, "t1")
	if cs := getMust(t, "t1", 0); len(cs) != 0 {
		t.Fatalf("v0 not empty: %v", cs)
	}
	cs := []cue.Cue{{Start: 0, End: 1000, Text: "hello"}}
	v1, err := Commit("t1", 0, cs)
	if err != nil || v1 != 1 {
		t.Fatalf("commit v1=%d err=%v", v1, err)
	}
	// stale base -> conflict
	if _, err := Commit("t1", 0, cs); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale commit: %v", err)
	}
	sum, err := Retime("t1", 1, exampleList)
	if err != nil || sum.Version != 2 || sum.Splits != 0 || sum.Dropped != 0 {
		t.Fatalf("retime %+v err=%v", sum, err)
	}
	if got := getMust(t, "t1", 2); len(got) != 1 || got[0] != cs[0] {
		t.Fatalf("cue outside edit span must be unchanged: %v", got)
	}
	// immutability: mutating the input slice must not change the version
	cs[0].Text = "mutated"
	if got := getMust(t, "t1", 1); got[0].Text != "hello" {
		t.Fatalf("version mutated: %v", got)
	}
	// returned slice is independent
	got := getMust(t, "t1", 2)
	got[0].Text = "x"
	if getMust(t, "t1", 2)[0].Text != "hello" {
		t.Fatal("Get must return a defensive copy")
	}
}

func TestCreateTrackErrors(t *testing.T) {
	resetTracks()
	if err := CreateTrack("", 500); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("empty id: %v", err)
	}
	if err := CreateTrack("a", 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("bad dmin: %v", err)
	}
	if err := CreateTrack("a", 500); err != nil {
		t.Fatal(err)
	}
	if err := CreateTrack("a", 500); !errors.Is(err, ErrTrackExist) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := Commit("missing", 0, nil); !errors.Is(err, ErrTrackNotFound) {
		t.Fatalf("missing track: %v", err)
	}
}

func TestVersionNotFound(t *testing.T) {
	fresh(t, "t")
	if _, err := Get("t", 1); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("get future: %v", err)
	}
	if _, err := Get("t", -1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("get negative must be invalid param: %v", err)
	}
	if _, err := Commit("t", 5, nil); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("commit future: %v", err)
	}
	if _, err := Retime("t", 5, edit.EditList{}); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("retime future: %v", err)
	}
}

func TestCueInvalidRejectionOrder(t *testing.T) {
	fresh(t, "t")
	// cue invalidity comes after version checks, and leaves no state.
	bad := []cue.Cue{{Start: 100, End: 99, Text: ""}}
	var ce *cue.CueError
	_, err := Commit("t", 0, bad)
	if !errors.As(err, &ce) || ce.Index != 0 || ce.Kind != cue.KindTime {
		t.Fatalf("want cue error time, got %v", err)
	}
	if cs := getMust(t, "t", 0); len(cs) != 0 {
		t.Fatal("rejected commit changed state")
	}
}

func TestParamBeforeTrackBeforeVersion(t *testing.T) {
	resetTracks()
	// invalid param beats missing track
	if _, err := Commit("", 0, nil); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("param>track: %v", err)
	}
	fresh(t, "t")
	// invalid edit list beats version lookup
	badEdit := edit.EditList{Insertions: []edit.Insertion{{At: 0, Len: 0}}}
	if _, err := Retime("t", 9, badEdit); !errors.Is(err, edit.ErrInvalidParam) {
		t.Fatalf("param>version: %v", err)
	}
	// version not found beats conflict
	if _, err := Commit("t", 3, []cue.Cue{{Start: 0, End: 500, Text: "a"}}); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("version>conflict: %v", err)
	}
}

func TestRebaseScenario(t *testing.T) {
	fresh(t, "t")
	// v3: ordinary commit after two plain versions, following the spec story.
	Commit("t", 0, []cue.Cue{{Start: 0, End: 500, Text: "v3"}})
	Commit("t", 1, []cue.Cue{{Start: 0, End: 500, Text: "v3"}, {Start: 1000, End: 1500, Text: "v3b"}})
	Commit("t", 2, []cue.Cue{{Start: 7000, End: 9000, Text: "v3text"}})
	// v4: retime
	sum, err := Retime("t", 3, exampleList)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Version != 4 || sum.Splits != 1 || sum.Dropped != 0 {
		t.Fatalf("v4 retime summary %+v", sum)
	}
	v4 := getMust(t, "t", 4)
	if len(v4) != 2 || v4[0] != (cue.Cue{Start: 4000, End: 5000, Text: "v3text"}) || v4[1] != (cue.Cue{Start: 6000, End: 7000, Text: "v3text"}) {
		t.Fatalf("v4 content %+v", v4)
	}
	// plain commit on stale base 3 -> conflict
	if _, err := Commit("t", 3, []cue.Cue{{Start: 7000, End: 9000, Text: "A"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("A plain stale: %v", err)
	}
	// invalid cues are validated on the base timeline and rejected, no new version
	if _, err := CommitRebased("t", 3, []cue.Cue{{Start: 8000, End: 8100, Text: "x"}}); !errors.Is(err, cue.ErrInvalidParam) {
		var ce *cue.CueError
		if !errors.As(err, &ce) {
			t.Fatalf("want CueError from rebase: %v", err)
		}
	}
	if len(getMust(t, "t", 4)) != 2 {
		t.Fatal("failed rebase changed state")
	}
	// A rebases across the single retime version -> v5
	v5, err := CommitRebased("t", 3, []cue.Cue{{Start: 7000, End: 9000, Text: "A"}})
	if err != nil || v5 != 5 {
		t.Fatalf("A rebase v5=%d err=%v", v5, err)
	}
	got5 := getMust(t, "t", 5)
	if len(got5) != 2 || got5[0].Text != "A" || got5[0] != (cue.Cue{Start: 4000, End: 5000, Text: "A"}) {
		t.Fatalf("rebased content %+v", got5)
	}
	// B rebases from base 3 across v4(retime)+v5(commit) -> conflict
	if _, err := CommitRebased("t", 3, []cue.Cue{{Start: 0, End: 500, Text: "B"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("B must be blocked by ordinary v5: %v", err)
	}
}

func TestRebaseAcrossTwoRetimes(t *testing.T) {
	fresh(t, "t")
	baseCues := []cue.Cue{{Start: 0, End: 10000, Text: "z"}} // dmin 500
	v1, err := Commit("t", 0, baseCues)
	if err != nil {
		t.Fatal(err)
	}
	e1 := exampleList
	s1, err := Retime("t", v1, e1)
	if err != nil {
		t.Fatal(err)
	}
	// second edit: insert another segment later on the new timeline
	e2 := edit.EditList{Insertions: []edit.Insertion{{At: 500, Len: 400}}}
	s2, err := Retime("t", s1.Version, e2)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Version != 3 {
		t.Fatalf("want v3, got %d", s2.Version)
	}
	// Rebase the original base content across both retimes; must equal
	// applying e1 then e2 to the base cues manually.
	v, err := CommitRebased("t", 1, baseCues)
	if err != nil || v != 4 {
		t.Fatalf("two-retime rebase v=%d err=%v", v, err)
	}
	step1, err := edit.Retime(500, e1, baseCues)
	if err != nil {
		t.Fatal(err)
	}
	step2, err := edit.Retime(500, e2, step1.Cues)
	if err != nil {
		t.Fatal(err)
	}
	rebased := getMust(t, "t", 4)
	want := step2.Cues
	if len(rebased) != len(want) {
		t.Fatalf("rebase %+v != chained %+v", rebased, want)
	}
	for i := range want {
		if rebased[i] != want[i] {
			t.Fatalf("piece %d: %+v != %+v", i, rebased[i], want[i])
		}
	}
	// the new version is an ordinary commit and blocks further rebase across it
	if _, err := CommitRebased("t", 1, baseCues); !errors.Is(err, ErrConflict) {
		t.Fatalf("ordinary rebased version must block: %v", err)
	}
	// base == latest is equivalent to Commit
	v5, err := CommitRebased("t", 4, want)
	if err != nil || v5 != 5 {
		t.Fatalf("rebase at latest v=%d err=%v", v5, err)
	}
}

func TestRebaseDropsAtEachStep(t *testing.T) {
	fresh(t, "t")
	// dmin 500; a cue that survives e1 but not e2 must not reappear.
	Commit("t", 0, []cue.Cue{{Start: 0, End: 10000, Text: "q"}})
	e1 := exampleList
	s1, _ := Retime("t", 1, e1)
	e2 := edit.EditList{Deletions: []edit.Deletion{{A: 4000, B: 4600}}} // shrinks first piece below 500
	s2, err := Retime("t", s1.Version, e2)
	if err != nil {
		t.Fatal(err)
	}
	v, err := CommitRebased("t", 1, []cue.Cue{{Start: 0, End: 10000, Text: "q"}})
	if err != nil {
		t.Fatal(err)
	}
	rebased := getMust(t, "t", v)
	direct := getMust(t, "t", s2.Version)
	if len(rebased) != len(direct) {
		t.Fatalf("per-step drop mismatch %+v vs %+v", rebased, direct)
	}
}

func TestConcurrentCommits(t *testing.T) {
	fresh(t, "t")
	var wg sync.WaitGroup
	var mu sync.Mutex
	var oks, conflicts int
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := Commit("t", 0, []cue.Cue{{Start: int64(i) * 1000, End: int64(i)*1000 + 500, Text: "c"}})
			mu.Lock()
			switch {
			case err == nil:
				oks++
			case errors.Is(err, ErrConflict):
				conflicts++
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if oks != 1 || conflicts != 7 {
		t.Fatalf("want exactly 1 ok / 7 conflict, got %d / %d", oks, conflicts)
	}
}

func TestConcurrentDifferentTracks(t *testing.T) {
	resetTracks()
	for i := 0; i < 16; i++ {
		id := string(rune('a' + i))
		if err := CreateTrack(id, 500); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 16; i++ {
		id := string(rune('a' + i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Commit(id, 0, []cue.Cue{{Start: 0, End: 500, Text: "x"}}); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("parallel tracks: %v", err)
	}
}

func TestReplayDeterminism(t *testing.T) {
	run := func() [][]cue.Cue {
		resetTracks()
		CreateTrack("r", 500)
		v, _ := Commit("r", 0, []cue.Cue{{Start: 1000, End: 3000, Text: "a"}, {Start: 7000, End: 9000, Text: "b"}})
		s, _ := Retime("r", v, exampleList)
		rv, _ := CommitRebased("r", v, []cue.Cue{{Start: 7000, End: 9000, Text: "A"}})
		_ = s
		out := [][]cue.Cue{}
		for i := 0; i <= rv; i++ {
			cs, _ := Get("r", i)
			out = append(out, cs)
		}
		return out
	}
	a := run()
	b := run()
	if len(a) != len(b) {
		t.Fatal("replay length differs")
	}
	for v := range a {
		if len(a[v]) != len(b[v]) {
			t.Fatalf("v%d length differs", v)
		}
		for i := range a[v] {
			if a[v][i] != b[v][i] {
				t.Fatalf("v%d cue %d differs: %+v vs %+v", v, i, a[v][i], b[v][i])
			}
		}
	}
	t.Logf("replay contents: %+v", a)
}
