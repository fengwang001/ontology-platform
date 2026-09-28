package ontology

import (
	"bytes"
	"testing"
)

// newTestRegistry 构造一个带日志捕获的三列表：
//
//	v1: [#1=a(default="da"), #2=b(default="db"), #3=c(default="dc")]
func newTestRegistry(t *testing.T, maxVersions int) (*Registry, *bytes.Buffer) {
	t.Helper()
	var log bytes.Buffer
	r, err := NewRegistry(
		[]ColumnSpec{{Name: "a", Default: "da"}, {Name: "b", Default: "db"}, {Name: "c", Default: "dc"}},
		WithMaxVersions(maxVersions), WithLogWriter(&log),
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r, &log
}

func mustEvolve(t *testing.T, r *Registry, changes ...Change) Schema {
	t.Helper()
	s, err := r.Evolve(changes)
	if err != nil {
		t.Fatalf("Evolve(%v) unexpected error: %v", changes, err)
	}
	return s
}

func assertCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s, got nil", want)
	}
	if got := AsErrorCode(err); got != want {
		t.Fatalf("error code = %s, want %s (err=%v)", got, want, err)
	}
}

// 初始建表：标识从 1 连续分配，顺序即追加顺序。
func TestNewRegistry_AssignsStableIDs(t *testing.T) {
	r, _ := newTestRegistry(t, DefaultMaxVersions)
	s := r.Current()
	if s.Version != 1 || len(s.Columns) != 3 {
		t.Fatalf("current = v%d cols=%d, want v1 cols=3", s.Version, len(s.Columns))
	}
	for i, c := range s.Columns {
		if c.ID != ColumnID(i+1) || c.Name != []string{"a", "b", "c"}[i] {
			t.Fatalf("col[%d]=%+v, want id=%d name=%s", i, c, i+1, []string{"a", "b", "c"}[i])
		}
	}
}

// 删除一列后再新增同名列，必须得到全新标识，旧标识不复用。
func TestEvolve_DropThenAddSameName_GetsNewID(t *testing.T) {
	r, _ := newTestRegistry(t, DefaultMaxVersions)

	// v2: 删除 #2(b)。
	mustEvolve(t, r, Change{Kind: ChangeDrop, ID: 2})
	// v3: 再次新增名为 b 的列 -> 新标识 #4，追加在末尾。
	s3 := mustEvolve(t, r, Change{Kind: ChangeAdd, Name: "b", Default: "newb"})

	if len(s3.Columns) != 3 {
		t.Fatalf("v3 columns=%d, want 3: %+v", len(s3.Columns), s3.Columns)
	}
	if s3.Columns[0].ID != 1 || s3.Columns[1].ID != 3 || s3.Columns[2].ID != 4 {
		t.Fatalf("v3 ids=%v, want [1 3 4]", idsOf(s3.Columns))
	}
	if s3.Columns[2].Name != "b" || s3.Columns[2].Default != "newb" {
		t.Fatalf("re-added column = %+v, want name=b default=newb", s3.Columns[2])
	}
	// 标识 #2 永不复用：当前结构中不存在。
	if s3.Has(2) {
		t.Fatalf("dropped id #2 must not exist after re-adding same name")
	}
}

// 改名只改名字，标识不变。
func TestEvolve_Rename_KeepsID(t *testing.T) {
	r, _ := newTestRegistry(t, DefaultMaxVersions)
	s := mustEvolve(t, r, Change{Kind: ChangeRename, ID: 2, Name: "b2"})
	col, ok := s.Column(2)
	if !ok || col.Name != "b2" {
		t.Fatalf("id #2 after rename = %+v ok=%v, want name=b2", col, ok)
	}
	if s.Has(ColumnID(2)) == false {
		t.Fatalf("renamed id #2 must still exist")
	}
}

// 解码：旧版本事件按稳定标识映射；删除后新增的同名列取当前默认值，
// 而不是错误地按位置/名字吃到旧列的值。
func TestDecode_MapsByStableID_AcrossDropAndReadd(t *testing.T) {
	r, _ := newTestRegistry(t, DefaultMaxVersions)
	mustEvolve(t, r, Change{Kind: ChangeDrop, ID: 2})                  // v2: a,c
	mustEvolve(t, r, Change{Kind: ChangeAdd, Name: "b", Default: "D"}) // v3: a(#1),c(#3),b(#4)

	// v1 事件：a=1, b(old)=2, c=3。v1 的 b 是 #2，已删除。
	row, err := r.Decode(Event{Version: 1, Values: []string{"1", "2", "3"}})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if row.SchemaVersion != 3 {
		t.Fatalf("row schema=%d, want 3", row.SchemaVersion)
	}
	want := []struct {
		id     ColumnID
		value  string
		source ValueSource
	}{
		{1, "1", SourceEvent},   // #1 a 按标识取到事件第 1 位
		{3, "3", SourceEvent},   // #3 c 按标识取到事件第 3 位（不是第 2 位）
		{4, "D", SourceDefault}, // #4 b 是删除后新增的同名列，取当前默认值
	}
	for _, w := range want {
		v, src, ok := row.Get(w.id)
		if !ok || v != w.value || src != w.source {
			t.Fatalf("id #%d = (%q,%v,%v), want (%q,%v,true)", w.id, v, src, ok, w.value, w.source)
		}
	}
	// 已删除的 #2 不出现在当前结果中。
	if row.Present(2) {
		t.Fatalf("dropped #2 must not be present in decoded row")
	}
}

// 空串是来自事件的合法值；缺列则取当前默认值，二者必须可区分。
func TestDecode_EmptyStringVsMissing(t *testing.T) {
	r, _ := newTestRegistry(t, DefaultMaxVersions)
	mustEvolve(t, r, Change{Kind: ChangeAdd, Name: "d", Default: "def-d"}) // v2: a,b,c,d

	// 给 v2 列 d 一个非空默认值，再新增 v3 列 e，默认值为空串。
	mustEvolve(t, r, Change{Kind: ChangeAdd, Name: "e", Default: ""}) // v3

	// v2 事件：a="", b="", c="x", d=""。全部来自事件（含空串）。
	row, err := r.Decode(Event{Version: 2, Values: []string{"", "", "x", ""}})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, id := range []ColumnID{1, 2, 4} {
		v, src, ok := row.Get(id)
		if !ok || v != "" || src != SourceEvent {
			t.Fatalf("id #%d = (%q,%v,%v), want empty from event", id, v, src, ok)
		}
	}
	// #5 e 在 v2 尚不存在 -> 缺列，取 v3 当前默认值（恰好也是空串），
	// 但来源必须是 SourceDefault，以与“事件中的空串”区分。
	v, src, ok := row.Get(5)
	if !ok || v != "" || src != SourceDefault {
		t.Fatalf("id #5 = (%q,%v,%v), want empty from current default", v, src, ok)
	}
}

// 按位置映射会出错的场景：列删除导致后续列位置左移，
// 正确实现仍按标识取值。
func TestDecode_PositionMappingWouldBeWrong(t *testing.T) {
	r, _ := newTestRegistry(t, DefaultMaxVersions)
	mustEvolve(t, r, Change{Kind: ChangeDrop, ID: 1}) // v2: b(#2), c(#3)

	// v1 事件 a=av,b=bv,c=cv。删除 a 后，若按当前位置取：
	// 位置0会错配成 av 给 b；正确做法 #2 应取 bv。
	row, err := r.Decode(Event{Version: 1, Values: []string{"av", "bv", "cv"}})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v, _, _ := row.Get(2); v != "bv" {
		t.Fatalf("id #2 = %q, want bv (stable id, not position)", v)
	}
	if v, _, _ := row.Get(3); v != "cv" {
		t.Fatalf("id #3 = %q, want cv (stable id, not position)", v)
	}
}

// 按名字映射会出错的场景：删除后新增同名列承载新语义/新默认值，
// 旧事件中同名字的值不得灌进新列。
func TestDecode_NameMappingWouldBeWrong(t *testing.T) {
	r, _ := newTestRegistry(t, DefaultMaxVersions)
	mustEvolve(t, r, Change{Kind: ChangeDrop, ID: 2})                   // 删除 b
	mustEvolve(t, r, Change{Kind: ChangeAdd, Name: "b", Default: "NB"}) // 新 b = #4

	row, err := r.Decode(Event{Version: 1, Values: []string{"a1", "OLD_B", "c1"}})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// 若按名字映射，新 b 会错误得到 OLD_B；正确结果应取当前默认值 NB。
	v, src, ok := row.Get(4)
	if !ok || v != "NB" || src != SourceDefault {
		t.Fatalf("re-added same-name id #4 = (%q,%v,%v), want NB from default", v, src, ok)
	}
}

// ---- 非法输入：可区分的拒绝原因 ----

func TestNewRegistry_InvalidInitialColumns(t *testing.T) {
	cases := []struct {
		name    string
		columns []ColumnSpec
	}{
		{"empty", nil},
		{"empty name", []ColumnSpec{{Name: ""}}},
		{"duplicate name", []ColumnSpec{{Name: "x"}, {Name: "x"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewRegistry(tc.columns)
			assertCode(t, err, ErrInvalidInitialColumns)
		})
	}
}

func TestDecode_VersionNotFound(t *testing.T) {
	r, _ := newTestRegistry(t, DefaultMaxVersions)
	for _, v := range []int{0, -1, 2, 99} {
		_, err := r.Decode(Event{Version: v, Values: []string{"a", "b", "c"}})
		assertCode(t, err, ErrVersionNotFound)
	}
}

func TestSchemaAt_VersionNotFound(t *testing.T) {
	r, _ := newTestRegistry(t, DefaultMaxVersions)
	if _, err := r.SchemaAt(7); AsErrorCode(err) != ErrVersionNotFound {
		t.Fatalf("SchemaAt(7) code=%v, want ErrVersionNotFound", AsErrorCode(err))
	}
}

func TestDecode_ValueCountMismatch(t *testing.T) {
	r, _ := newTestRegistry(t, DefaultMaxVersions)
	mustEvolve(t, r, Change{Kind: ChangeAdd, Name: "d", Default: ""}) // v2 有 4 列
	cases := [][]string{
		nil,
		{"a", "b"},           // 少于 v1 的 3 列
		{"a", "b", "c", "x"}, // v1 只有 3 列
	}
	for i, vals := range cases {
		_, err := r.Decode(Event{Version: 1, Values: vals})
		if AsErrorCode(err) != ErrValueCountMismatch {
			t.Fatalf("case %d code=%v, want ErrValueCountMismatch (vals=%v)", i, AsErrorCode(err), vals)
		}
	}
}

func TestEvolve_InvalidChanges_LeaveStateUntouched(t *testing.T) {
	cases := []struct {
		name    string
		changes []Change
	}{
		{"empty batch", nil},
		{"unknown kind", []Change{{Kind: ChangeUnknown}}},
		{"add empty name", []Change{{Kind: ChangeAdd, Name: ""}}},
		{"add duplicate name", []Change{{Kind: ChangeAdd, Name: "a"}}},
		{"drop missing id", []Change{{Kind: ChangeDrop, ID: 99}}},
		{"drop already dropped", []Change{
			{Kind: ChangeDrop, ID: 2}, {Kind: ChangeDrop, ID: 2},
		}},
		{"rename missing id", []Change{{Kind: ChangeRename, ID: 99, Name: "z"}}},
		{"rename to empty", []Change{{Kind: ChangeRename, ID: 1, Name: ""}}},
		{"rename to existing name", []Change{{Kind: ChangeRename, ID: 1, Name: "b"}}},
		{"drop then readd conflicting in batch then fail", []Change{
			{Kind: ChangeDrop, ID: 2}, {Kind: ChangeAdd, Name: "a"},
		}},
		{"successful add then later failure must not consume id", []Change{
			{Kind: ChangeAdd, Name: "z", Default: ""}, // 预分配 #4 成功
			{Kind: ChangeDrop, ID: 99},                // 随后失败 -> 整批拒绝
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newTestRegistry(t, DefaultMaxVersions)
			beforeCount := r.VersionCount()
			before := r.Current()

			_, err := r.Evolve(tc.changes)
			assertCode(t, err, ErrInvalidChange)

			// 被拒绝后版本列表不变。
			if r.VersionCount() != beforeCount {
				t.Fatalf("version count changed after reject: %d -> %d", beforeCount, r.VersionCount())
			}
			after := r.Current()
			if !schemasEqual(before, after) {
				t.Fatalf("current schema changed after rejected evolve:\nbefore=%+v\nafter =%+v", before.Columns, after.Columns)
			}

			// 标识分配也不变：紧接着合法新增，仍应得到原顺序下的下一个 ID (#4)，
			// 即使被拒绝批次里曾“预分配”过标识。
			s := mustEvolve(t, r, Change{Kind: ChangeAdd, Name: "fresh", Default: ""})
			last := s.Columns[len(s.Columns)-1]
			if last.ID != 4 {
				t.Fatalf("id after rejected evolve = %d, want 4 (id allocation must not advance)", last.ID)
			}
		})
	}
}

// 同批“先增后改”合法，且提交后才推进标识。
func TestEvolve_AddThenRenameInSameBatch(t *testing.T) {
	r, _ := newTestRegistry(t, DefaultMaxVersions)
	s := mustEvolve(t, r,
		Change{Kind: ChangeAdd, Name: "tmp", Default: "t"},
		Change{Kind: ChangeRename, ID: 4, Name: "final"},
	)
	col, ok := s.Column(4)
	if !ok || col.Name != "final" {
		t.Fatalf("batch add-then-rename = %+v ok=%v, want #4 name=final", col, ok)
	}
	if r.VersionCount() != 2 {
		t.Fatalf("versions=%d, want 2 (batch is one new version)", r.VersionCount())
	}
}

// 批量解码：任一事件非法则整批失败，不返回部分结果。
func TestDecodeBatch_Atomic(t *testing.T) {
	r, _ := newTestRegistry(t, DefaultMaxVersions)
	good := Event{Version: 1, Values: []string{"1", "2", "3"}}
	badVersion := Event{Version: 42, Values: []string{"1", "2", "3"}}
	badCount := Event{Version: 1, Values: []string{"1"}}

	if rows, err := r.DecodeBatch([]Event{good, good}); err != nil || len(rows) != 2 {
		t.Fatalf("all-good batch = rows=%d err=%v", len(rows), err)
	}
	if rows, err := r.DecodeBatch([]Event{good, badVersion, good}); err == nil || rows != nil {
		t.Fatalf("bad-version batch must fail atomically: rows=%v err=%v", rows, err)
	} else if AsErrorCode(err) != ErrVersionNotFound {
		t.Fatalf("batch code=%v, want ErrVersionNotFound", AsErrorCode(err))
	}
	if rows, err := r.DecodeBatch([]Event{good, badCount}); err == nil || rows != nil {
		t.Fatalf("bad-count batch must fail atomically: rows=%v err=%v", rows, err)
	} else if AsErrorCode(err) != ErrValueCountMismatch {
		t.Fatalf("batch code=%v, want ErrValueCountMismatch", AsErrorCode(err))
	}
}

// 反复解码同一输入，结果完全一致。
func TestDecode_Deterministic(t *testing.T) {
	r, _ := newTestRegistry(t, DefaultMaxVersions)
	mustEvolve(t, r, Change{Kind: ChangeDrop, ID: 2})
	ev := Event{Version: 1, Values: []string{"x", "y", "z"}}
	first, err := r.Decode(ev)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := r.Decode(ev)
		if err != nil {
			t.Fatalf("decode %d: %v", i, err)
		}
		if !rowsEqual(first, again) {
			t.Fatalf("decode not deterministic on iteration %d", i)
		}
	}
}

// ---- 辅助 ----

func idsOf(cols []ColumnDef) []ColumnID {
	ids := make([]ColumnID, len(cols))
	for i, c := range cols {
		ids[i] = c.ID
	}
	return ids
}

func schemasEqual(a, b Schema) bool {
	if a.Version != b.Version || len(a.Columns) != len(b.Columns) {
		return false
	}
	for i := range a.Columns {
		if a.Columns[i] != b.Columns[i] {
			return false
		}
	}
	return true
}

func rowsEqual(a, b Row) bool {
	if a.SchemaVersion != b.SchemaVersion || len(a.Order) != len(b.Order) {
		return false
	}
	for _, id := range a.Order {
		va, sa, oka := a.Get(id)
		vb, sb, okb := b.Get(id)
		if va != vb || sa != sb || oka != okb {
			return false
		}
	}
	return true
}
