package track_test

import (
	"math/rand"
	"testing"

	"ontology/cue"
	"ontology/edit"
	"ontology/track"
)

// TestOutputDurationNeverExceedsInput：重定时前后按原字幕归组，输出片总时长
// 不超过原时长；并校验每个版本的字幕集合都合法。
func TestOutputDurationNeverExceedsInput(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	const dmin int64 = 2
	for it := 0; it < 300; it++ {
		tbl := edit.Table{}
		// 小规模随机合法表。
		a := r.Int63n(30)
		b := a + 1 + r.Int63n(20)
		tbl.Deletes = append(tbl.Deletes, edit.Delete{A: a, B: b})
		at := r.Int63n(60)
		if !(a < at && at < b) {
			tbl.Inserts = append(tbl.Inserts, edit.Insert{At: at, Len: 1 + r.Int63n(3)})
		}
		c, err := edit.Compile(tbl)
		if err != nil {
			t.Fatalf("compile %+v: %v", tbl, err)
		}
		var cs []cue.Cue
		var cur int64
		for cur < 60 && len(cs) < 4 {
			s := cur
			e := s + dmin + r.Int63n(15)
			if e > 60 {
				e = 60
			}
			cs = append(cs, cue.Cue{Start: s, End: e, Text: "z"})
			cur = e
		}
		res := edit.Retime(c, cs, dmin)
		// 用朴素方式按新旧坐标邻近关系归组：输出片数不超过"原片数+切分数"，
		// 这里直接核对总时长。
		var inDur, outDur int64
		for _, x := range cs {
			inDur += x.End - x.Start
		}
		for _, x := range res.Cues {
			outDur += x.End - x.Start
		}
		if outDur > inDur {
			t.Fatalf("case %d: out duration %d > in %d", it, outDur, inDur)
		}
		if err := cue.Validate(res.Cues, dmin); err != nil {
			t.Fatalf("case %d output invalid: %v\n%+v", it, err, res.Cues)
		}
	}
}

// TestReplayDeterminism：相同操作序列重放得到逐条相同的版本内容。
func TestReplayDeterminism(t *testing.T) {
	play := func(id string) [][]cue.Cue {
		if err := track.CreateTrack(id); err != nil {
			t.Fatal(err)
		}
		var snaps [][]cue.Cue
		v0, _ := track.Get(id, 0)
		snaps = append(snaps, v0.Cues)
		if _, err := track.Commit(id, 0, []cue.Cue{mkCue(0, 20000, "a"), mkCue(20000, 30000, "b")}); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := track.Retime(id, 1, specTable()); err != nil {
			t.Fatal(err)
		}
		if _, err := track.CommitRebased(id, 1, []cue.Cue{mkCue(0, 9000, "c")}); err != nil {
			t.Fatal(err)
		}
		for v := 0; v <= 3; v++ {
			got, err := track.Get(id, v)
			if err != nil {
				t.Fatal(err)
			}
			snaps = append(snaps, got.Cues)
		}
		return snaps
	}
	s1 := play("rep-1")
	s2 := play("rep-2")
	if len(s1) != len(s2) {
		t.Fatalf("snapshot count differs")
	}
	for i := range s1 {
		if len(s1[i]) != len(s2[i]) {
			t.Fatalf("version %d len differs", i)
		}
		for j := range s1[i] {
			if s1[i][j] != s2[i][j] {
				t.Fatalf("v%d cue %d: %+v != %+v", i, j, s1[i][j], s2[i][j])
			}
		}
	}
}
