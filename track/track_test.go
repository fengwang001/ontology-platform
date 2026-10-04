package track_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/cue"
	"ontology/edit"
	"ontology/track"
)

func mkCue(s, e int64, text string) cue.Cue { return cue.Cue{Start: s, End: e, Text: text} }

func freshTrack(t *testing.T, id string, dmin ...int64) {
	t.Helper()
	if err := track.CreateTrack(id, dmin...); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
}

func TestCreateAndEmptyV0(t *testing.T) {
	freshTrack(t, "t0")
	v, err := track.Get("t0", 0)
	if err != nil || v.Number != 0 || len(v.Cues) != 0 {
		t.Fatalf("v0: %+v err=%v", v, err)
	}
	if err := track.CreateTrack("t0"); !errors.Is(err, track.ErrTrackExists) {
		t.Fatalf("dup create: %v", err)
	}
}

func TestCommitChain(t *testing.T) {
	freshTrack(t, "t1")
	v1, err := track.Commit("t1", 0, []cue.Cue{mkCue(0, 500, "a")})
	if err != nil || v1 != 1 {
		t.Fatalf("commit v1: %d %v", v1, err)
	}
	if _, err := track.Commit("t1", 0, []cue.Cue{mkCue(0, 500, "stale")}); !errors.Is(err, track.ErrConflict) {
		t.Fatalf("stale base commit: %v", err)
	}
	v2, err := track.Commit("t1", 1, []cue.Cue{mkCue(0, 600, "b")})
	if err != nil || v2 != 2 {
		t.Fatalf("commit v2: %d %v", v2, err)
	}
	v, err := track.Get("t1", 1)
	if err != nil || v.Cues[0].Text != "a" {
		t.Fatalf("get v1: %+v %v", v, err)
	}
}

func specTable() edit.Table {
	return edit.Table{
		Deletes: []edit.Delete{{A: 2000, B: 5000}},
		Inserts: []edit.Insert{{At: 8000, Len: 1000}},
	}
}

func TestRetimeVersion(t *testing.T) {
	freshTrack(t, "t2")
	if _, err := track.Commit("t2", 0, []cue.Cue{
		mkCue(1000, 3000, "a"),
		mkCue(3500, 4800, "gone"),
		mkCue(7000, 9000, "split"),
	}); err != nil {
		t.Fatal(err)
	}
	v, splits, dropped, err := track.Retime("t2", 1, specTable())
	if err != nil || v != 2 || splits != 1 || dropped != 1 {
		t.Fatalf("retime v=%d splits=%d dropped=%d err=%v", v, splits, dropped, err)
	}
	got, _ := track.Get("t2", 2)
	want := []cue.Cue{
		mkCue(1000, 2000, "a"),
		mkCue(4000, 5000, "split"),
		mkCue(6000, 7000, "split"),
	}
	if len(got.Cues) != len(want) {
		t.Fatalf("got %+v want %+v", got.Cues, want)
	}
	for i := range want {
		if got.Cues[i] != want[i] {
			t.Fatalf("piece %d: got %+v want %+v", i, got.Cues[i], want[i])
		}
	}
}

func TestRebaseAcrossRetimes(t *testing.T) {
	freshTrack(t, "t3")
	for i := 0; i < 3; i++ {
		if _, err := track.Commit("t3", i, []cue.Cue{mkCue(0, 500, fmt.Sprintf("c%d", i))}); err != nil {
			t.Fatal(err)
		}
	}
	v4, _, _, err := track.Retime("t3", 3, specTable())
	if err != nil || v4 != 4 {
		t.Fatalf("retime: %d %v", v4, err)
	}
	if _, err := track.Commit("t3", 3, []cue.Cue{mkCue(7000, 9000, "a")}); !errors.Is(err, track.ErrConflict) {
		t.Fatalf("commit across retime: %v", err)
	}
	v5, err := track.CommitRebased("t3", 3, []cue.Cue{mkCue(7000, 9000, "a")})
	if err != nil || v5 != 5 {
		t.Fatalf("rebase: %d %v", v5, err)
	}
	got, _ := track.Get("t3", 5)
	want := []cue.Cue{mkCue(4000, 5000, "a"), mkCue(6000, 7000, "a")}
	if len(got.Cues) != 2 || got.Cues[0] != want[0] || got.Cues[1] != want[1] {
		t.Fatalf("rebased v5: %+v", got.Cues)
	}
	if _, err := track.CommitRebased("t3", 3, []cue.Cue{mkCue(0, 500, "b")}); !errors.Is(err, track.ErrConflict) {
		t.Fatalf("rebase across normal commit must conflict: %v", err)
	}
	v6, err := track.CommitRebased("t3", 5, []cue.Cue{mkCue(0, 500, "c")})
	if err != nil || v6 != 6 {
		t.Fatalf("rebase at head: %d %v", v6, err)
	}
}

func TestRebaseAcrossTwoConsecutiveRetimes(t *testing.T) {
	freshTrack(t, "t4")
	if _, err := track.Commit("t4", 0, []cue.Cue{mkCue(0, 20000, "x")}); err != nil {
		t.Fatal(err)
	}
	tbl1 := edit.Table{Deletes: []edit.Delete{{A: 2000, B: 5000}}, Inserts: []edit.Insert{{At: 8000, Len: 1000}}}
	tbl2 := edit.Table{Deletes: []edit.Delete{{A: 1000, B: 1500}}}
	if _, _, _, err := track.Retime("t4", 1, tbl1); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := track.Retime("t4", 2, tbl2); err != nil {
		t.Fatal(err)
	}
	in := []cue.Cue{mkCue(0, 20000, "y")}
	c1, _ := edit.Compile(tbl1)
	c2, _ := edit.Compile(tbl2)
	step1 := edit.Retime(c1, in, track.DefaultDMin).Cues
	want := edit.Retime(c2, step1, track.DefaultDMin).Cues
	v, err := track.CommitRebased("t4", 1, in)
	if err != nil || v != 4 {
		t.Fatalf("double rebase: %d %v", v, err)
	}
	got, _ := track.Get("t4", 4)
	if len(got.Cues) != len(want) {
		t.Fatalf("got %+v want %+v", got.Cues, want)
	}
	for i := range want {
		if got.Cues[i] != want[i] {
			t.Fatalf("piece %d got %+v want %+v", i, got.Cues[i], want[i])
		}
	}
}

func TestRebaseInvalidCues(t *testing.T) {
	freshTrack(t, "t5")
	if _, err := track.Commit("t5", 0, []cue.Cue{mkCue(0, 10000, "x")}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := track.Retime("t5", 1, specTable()); err != nil {
		t.Fatal(err)
	}
	_, err := track.CommitRebased("t5", 1, []cue.Cue{mkCue(0, 10, "s")})
	var inv *cue.InvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("want cue invalid, got %v", err)
	}
	if _, err := track.Get("t5", 3); !errors.Is(err, track.ErrVersionNotFound) {
		t.Fatalf("no version may be created: %v", err)
	}
}

func TestRejectionOrder(t *testing.T) {
	freshTrack(t, "t6")
	if err := track.CreateTrack(""); !errors.Is(err, track.ErrInvalidArgument) {
		t.Fatalf("empty id: %v", err)
	}
	if err := track.CreateTrack("bad-dmin", 0); !errors.Is(err, track.ErrInvalidArgument) {
		t.Fatalf("bad dmin: %v", err)
	}
	if _, err := track.Commit("missing", 0, nil); !errors.Is(err, track.ErrTrackNotFound) {
		t.Fatalf("missing track: %v", err)
	}
	if _, err := track.Commit("t6", 5, nil); !errors.Is(err, track.ErrVersionNotFound) {
		t.Fatalf("future base: %v", err)
	}
	if _, err := track.Commit("t6", -1, nil); !errors.Is(err, track.ErrInvalidArgument) {
		t.Fatalf("negative base is invalid arg: %v", err)
	}
	if _, err := track.Get("t6", -1); !errors.Is(err, track.ErrVersionNotFound) {
		t.Fatalf("get negative: %v", err)
	}
	if _, err := track.Get("t6", 99); !errors.Is(err, track.ErrVersionNotFound) {
		t.Fatalf("get future: %v", err)
	}
	if _, err := track.Commit("t6", 0, []cue.Cue{mkCue(0, 500, "x")}); err != nil {
		t.Fatal(err)
	}
	if _, err := track.Commit("t6", 0, []cue.Cue{mkCue(0, 1, "x")}); !errors.Is(err, track.ErrConflict) {
		t.Fatalf("conflict before cue invalid: %v", err)
	}
	_, _, _, err := track.Retime("t6", 1, edit.Table{Deletes: []edit.Delete{{A: 5, B: 5}}})
	if !errors.Is(err, track.ErrInvalidArgument) {
		t.Fatalf("bad table: %v", err)
	}
	if _, err := track.Get("t6", 1); err != nil {
		t.Fatalf("state changed: %v", err)
	}
}

func TestRejectedOpsLeaveStateIntact(t *testing.T) {
	freshTrack(t, "t7")
	if _, err := track.Commit("t7", 0, []cue.Cue{mkCue(0, 500, "ok")}); err != nil {
		t.Fatal(err)
	}
	before, _ := track.Get("t7", 1)
	_, _, _, _ = track.Retime("t7", 1, edit.Table{Inserts: []edit.Insert{{At: 0, Len: 0}}})
	_, _ = track.Commit("t7", 1, []cue.Cue{mkCue(0, 1, "")})
	_, _ = track.CommitRebased("t7", 1, []cue.Cue{mkCue(0, 1, "")})
	after, _ := track.Get("t7", 1)
	if len(after.Cues) != 1 || after.Cues[0] != before.Cues[0] {
		t.Fatalf("state mutated by rejected ops: %+v", after.Cues)
	}
	if _, err := track.Get("t7", 2); !errors.Is(err, track.ErrVersionNotFound) {
		t.Fatalf("version created by rejected op: %v", err)
	}
}

func TestGetReturnsDefensiveCopy(t *testing.T) {
	freshTrack(t, "t8")
	if _, err := track.Commit("t8", 0, []cue.Cue{mkCue(0, 500, "a")}); err != nil {
		t.Fatal(err)
	}
	v1, _ := track.Get("t8", 1)
	v1.Cues[0] = mkCue(0, 999, "tampered")
	v2, _ := track.Get("t8", 1)
	if v2.Cues[0].Text != "a" || v2.Cues[0].End != 500 {
		t.Fatalf("internal state leaked: %+v", v2.Cues)
	}
}

func TestConcurrentSameBaseOneWinner(t *testing.T) {
	freshTrack(t, "tc")
	const n = 64
	var wg sync.WaitGroup
	results := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, err := track.Commit("tc", 0, []cue.Cue{mkCue(0, 500, "c")})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, track.ErrConflict):
			conflict++
		default:
			t.Fatalf("unexpected err %v", err)
		}
	}
	if ok != 1 || conflict != n-1 {
		t.Fatalf("ok=%d conflict=%d", ok, conflict)
	}
}

func TestConcurrentDifferentTracks(t *testing.T) {
	const n = 16
	for i := 0; i < n; i++ {
		freshTrack(t, fmt.Sprintf("par-%d", i))
	}
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("par-%d", i)
			for v := 0; v < 10; v++ {
				if _, err := track.Commit(id, v, []cue.Cue{mkCue(0, 500, "z")}); err != nil {
					t.Errorf("track %s v%d: %v", id, v, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
