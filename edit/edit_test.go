package edit_test

import (
	"errors"
	"testing"

	"ontology/cue"
	"ontology/edit"
)

func mustCompile(t *testing.T, tbl edit.Table) *edit.Compiled {
	t.Helper()
	c, err := edit.Compile(tbl)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return c
}

func exampleTable() edit.Table {
	return edit.Table{
		Deletes: []edit.Delete{{A: 2000, B: 5000}},
		Inserts: []edit.Insert{{At: 8000, Len: 1000}},
	}
}

func TestMappingsSpecExample(t *testing.T) {
	c := mustCompile(t, exampleTable())
	// del(t)
	delCases := []struct{ t, want int64 }{
		{0, 0}, {1000, 0}, {2000, 0}, {3000, 1000}, {5000, 3000}, {9000, 3000},
	}
	for _, d := range delCases {
		if got := c.Del(d.t); got != d.want {
			t.Errorf("Del(%d)=%d want %d", d.t, got, d.want)
		}
	}
	// insL/insR 在插入点两侧的不对称。
	if c.InsL(8000) != 0 || c.InsR(8000) != 1000 {
		t.Errorf("at=8000: insL=%d insR=%d", c.InsL(8000), c.InsR(8000))
	}
	if c.InsL(8001) != 1000 || c.InsR(7999) != 0 {
		t.Errorf("around 8000: insL(8001)=%d insR(7999)=%d", c.InsL(8001), c.InsR(7999))
	}
}

func TestRetimeSpecExample(t *testing.T) {
	const dmin int64 = 500
	c := mustCompile(t, exampleTable())
	tests := []struct {
		name    string
		in      cue.Cue
		want    []cue.Cue
		splits  int
		dropped int
	}{
		{
			"delete cuts left part",
			cue.Cue{Start: 1000, End: 3000, Text: "a"},
			[]cue.Cue{{Start: 1000, End: 2000, Text: "a"}}, 0, 0,
		},
		{
			"fully deleted zero length",
			cue.Cue{Start: 2500, End: 4800, Text: "b"}, nil, 0, 1,
		},
		{
			"partially deleted 400 dropped",
			cue.Cue{Start: 4700, End: 5400, Text: "c"}, nil, 0, 1,
		},
		{
			"partially deleted exactly 500 kept",
			cue.Cue{Start: 4500, End: 5500, Text: "d"},
			[]cue.Cue{{Start: 2000, End: 2500, Text: "d"}}, 0, 0,
		},
		{
			"internal insert splits",
			cue.Cue{Start: 7000, End: 9000, Text: "e"},
			[]cue.Cue{{Start: 4000, End: 5000, Text: "e"}, {Start: 6000, End: 7000, Text: "e"}}, 1, 0,
		},
		{
			"insert at start shifts whole cue",
			cue.Cue{Start: 8000, End: 8600, Text: "f"},
			[]cue.Cue{{Start: 6000, End: 6600, Text: "f"}}, 0, 0,
		},
		{
			"insert at end does not extend",
			cue.Cue{Start: 7500, End: 8000, Text: "g"},
			[]cue.Cue{{Start: 4500, End: 5000, Text: "g"}}, 0, 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := edit.Retime(c, []cue.Cue{tc.in}, dmin)
			if r.Splits != tc.splits || r.Dropped != tc.dropped {
				t.Fatalf("splits=%d dropped=%d want %d/%d", r.Splits, r.Dropped, tc.splits, tc.dropped)
			}
			assertCues(t, r.Cues, tc.want)
		})
	}
}

func assertCues(t *testing.T, got, want []cue.Cue) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("piece %d got %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestDeleteThroughMiddleNoSplit(t *testing.T) {
	// 删除段贯穿字幕中间，不切分：[1000,9000) 删 [3000,7000)
	// -> [1000, 5000) 单片。
	c := mustCompile(t, edit.Table{Deletes: []edit.Delete{{A: 3000, B: 7000}}})
	r := edit.Retime(c, []cue.Cue{{Start: 1000, End: 9000, Text: "x"}}, 500)
	assertCues(t, r.Cues, []cue.Cue{{Start: 1000, End: 5000, Text: "x"}})
	if r.Splits != 0 {
		t.Fatalf("splits=%d", r.Splits)
	}
}

func TestDeleteCoversEnds(t *testing.T) {
	c := mustCompile(t, edit.Table{Deletes: []edit.Delete{{A: 3000, B: 7000}}})
	// 删除覆盖字幕终点：[1000,5000) -> [1000,3000)
	r := edit.Retime(c, []cue.Cue{{Start: 1000, End: 5000, Text: "a"}}, 500)
	assertCues(t, r.Cues, []cue.Cue{{Start: 1000, End: 3000, Text: "a"}})
	// 删除覆盖字幕起点：[5000,9000) -> [3000,5000)
	r = edit.Retime(c, []cue.Cue{{Start: 5000, End: 9000, Text: "b"}}, 500)
	assertCues(t, r.Cues, []cue.Cue{{Start: 3000, End: 5000, Text: "b"}})
}

func TestMultipleInternalInserts(t *testing.T) {
	c := mustCompile(t, edit.Table{Inserts: []edit.Insert{
		{At: 200, Len: 100}, {At: 400, Len: 100}, {At: 600, Len: 100},
	}})
	r := edit.Retime(c, []cue.Cue{{Start: 100, End: 700, Text: "x"}}, 50)
	want := []cue.Cue{
		{Start: 100, End: 200, Text: "x"}, {Start: 300, End: 500, Text: "x"}, {Start: 600, End: 800, Text: "x"}, {Start: 900, End: 1000, Text: "x"},
	}
	assertCues(t, r.Cues, want)
	if r.Splits != 3 {
		t.Fatalf("splits=%d want 3", r.Splits)
	}
}

func TestInsertAtDeleteEndpoints(t *testing.T) {
	// 插入点恰在删除段端点 a 与 b：合法且映射不对称正确。
	tbl := edit.Table{
		Deletes: []edit.Delete{{A: 2000, B: 5000}},
		Inserts: []edit.Insert{{At: 2000, Len: 1000}, {At: 5000, Len: 1000}},
	}
	c := mustCompile(t, tbl)
	// 字幕 [1000,2000) 终点恰为 at=a：终点插入不延长。
	// fR(1000)=1000, fL(2000)=2000（insL 不含 at=2000）-> [1000,2000)
	r := edit.Retime(c, []cue.Cue{{Start: 1000, End: 2000, Text: "l"}}, 500)
	assertCues(t, r.Cues, []cue.Cue{{Start: 1000, End: 2000, Text: "l"}})
	// 字幕 [5000,6000) 起点恰为 at=b：起点插入整体右移。
	// fR(5000)=5000-3000+2000=4000, fL(6000)=6000-3000+2000=5000
	r = edit.Retime(c, []cue.Cue{{Start: 5000, End: 6000, Text: "r"}}, 500)
	assertCues(t, r.Cues, []cue.Cue{{Start: 4000, End: 5000, Text: "r"}})
}

func TestInvalidTables(t *testing.T) {
	bad := []edit.Table{
		{Deletes: []edit.Delete{{A: 5, B: 5}}},
		{Deletes: []edit.Delete{{5, 3}}},
		{Deletes: []edit.Delete{{-1, 5}}},
		{Deletes: []edit.Delete{{1_000_000_000, 1_000_000_001}}},
		{Deletes: []edit.Delete{{0, 10}, {5, 20}}},                        // 重叠
		{Inserts: []edit.Insert{{5, 0}}},                                  // len<1
		{Inserts: []edit.Insert{{5, 1}, {5, 1}}},                          // at 相同
		{Inserts: []edit.Insert{{6, 1}, {5, 1}}},                          // 未升序
		{Deletes: []edit.Delete{{0, 10}}, Inserts: []edit.Insert{{5, 1}}}, // at 在内部
	}
	for i, tbl := range bad {
		if _, err := edit.Compile(tbl); !errors.Is(err, edit.ErrInvalidTable) {
			t.Errorf("case %d: want ErrInvalidTable, got %v", i, err)
		}
	}
	// at 恰等于 a、b 合法。
	good := edit.Table{
		Deletes: []edit.Delete{{10, 20}},
		Inserts: []edit.Insert{{At: 10, Len: 1}, {At: 20, Len: 1}},
	}
	if _, err := edit.Compile(good); err != nil {
		t.Fatalf("endpoint inserts should be valid: %v", err)
	}
}
