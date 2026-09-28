package ontology

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// ---- 辅助 -------------------------------------------------------------------

func mustRegistry(t *testing.T, cols []ColumnSpec, opts ...Option) *Registry {
	t.Helper()
	r, err := NewRegistry(cols, opts...)
	if err != nil {
		t.Fatalf("NewRegistry unexpected error: %v", err)
	}
	return r
}

func cellByID(row Row, id ColumnID) (Cell, bool) {
	for _, c := range row.Cells() {
		if c.ID == id {
			return c, true
		}
	}
	return Cell{}, false
}

func wantErrKind(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", kind)
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *ontology.Error, got %T: %v", err, err)
	}
	if e.Kind != kind {
		t.Fatalf("expected error kind %s, got %s (%v)", kind, e.Kind, err)
	}
}

// ---- 初始化非法输入 ----------------------------------------------------------

func TestNewRegistry_InvalidSchema(t *testing.T) {
	cases := []struct {
		name  string
		specs []ColumnSpec
	}{
		{"no columns", nil},
		{"empty name", []ColumnSpec{{Name: "a"}, {Name: ""}}},
		{"duplicate name", []ColumnSpec{{Name: "a"}, {Name: "a"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewRegistry(tc.specs)
			wantErrKind(t, err, KindInvalidSchema)
			if !errors.Is(err, ErrInvalidSchema) {
				t.Fatalf("errors.Is(err, ErrInvalidSchema) = false; err=%v", err)
			}
		})
	}

	if _, err := NewRegistry([]ColumnSpec{{Name: "a"}}, WithMaxVersions(0)); !errors.Is(err, ErrInvalidSchema) {
		t.Fatalf("max versions 0 should be rejected as invalid schema, got %v", err)
	}
}

// ---- 删除后再新增同名列得到全新标识、且标识永不复用 ------------------------------

func TestDropThenReaddSameName_GetsNewID(t *testing.T) {
	r := mustRegistry(t, []ColumnSpec{
		{Name: "a", Default: "DA"},
		{Name: "b", Default: "DB"},
	})

	c, err := r.AddColumn("c", "DC")
	if err != nil {
		t.Fatalf("add c: %v", err)
	}
	idC := c.Columns()[2].ID
	if _, err := r.DropColumn(2 /* id of b */); err != nil {
		t.Fatalf("drop b: %v", err)
	}
	readded, err := r.AddColumn("b", "DB2") // 与被删除列同名
	if err != nil {
		t.Fatalf("re-add b: %v", err)
	}

	var newID ColumnID
	for _, col := range readded.Columns() {
		if col.Name == "b" {
			newID = col.ID
		}
	}
	if newID == 0 {
		t.Fatal("re-added column b not found")
	}
	if newID == 2 {
		t.Fatalf("re-added column reused deleted id 2; got %d", newID)
	}
	if newID != idC+1 {
		t.Fatalf("new column should get fresh monotonic id %d, got %d", idC+1, newID)
	}

	// v1 事件 ["a1","b1"]：旧 b（id=2）已删除，其值不得泄漏到同名新列。
	row, err := r.Decode(1, []string{"a1", "b1"})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cell, ok := cellByID(row, newID); !ok || cell.Value != "DB2" || !cell.FromDefault {
		t.Fatalf("re-added same-name column must resolve to current default, got %+v ok=%v", cell, ok)
	}
	if _, ok := row.Value(2); ok {
		t.Fatal("deleted column id 2 must not appear in decoded row")
	}
	if cell, _ := cellByID(row, 1); cell.Value != "a1" || cell.FromDefault {
		t.Fatalf("surviving column must keep event value, got %+v", cell)
	}
}

// ---- 空串是合法值，且与缺列严格区分 ---------------------------------------------

func TestDecode_EmptyStringVsMissing(t *testing.T) {
	r := mustRegistry(t, []ColumnSpec{{Name: "a", Default: "DA"}})
	if _, err := r.AddColumn("b", "DB"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddColumn("c", ""); err != nil { // 默认值本身是空串
		t.Fatal(err)
	}

	// v1 事件，a 携带显式空串：a → ""（来自事件，非默认）；b、c 缺列 → 默认值。
	row, err := r.Decode(1, []string{""})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := cellByID(row, 1)
	b, _ := cellByID(row, 2)
	c, _ := cellByID(row, 3)

	if a.Value != "" || a.FromDefault {
		t.Fatalf("explicit empty string is a real value: got %+v", a)
	}
	if b.Value != "DB" || !b.FromDefault {
		t.Fatalf("missing column must take current default DB: got %+v", b)
	}
	if c.Value != "" || !c.FromDefault {
		t.Fatalf("missing column with empty default is still FromDefault: got %+v", c)
	}
	// 两种空串来源在值上相同，但 FromDefault 必须能区分。
	if a.Value != c.Value || a.FromDefault == c.FromDefault {
		t.Fatal("explicit empty string and defaulted empty string must be distinguishable")
	}
}

// ---- 按名字或按位置映射都会出错，只有按标识解码始终正确 ----------------------------

func TestDecode_NameAndPositionMappingAreWrong(t *testing.T) {
	// 场景一：朴素“按位置映射”会把已删除列的旧值错配给存活列。
	// v1: a(id1)@0, b(id2)@1, c(id3)@2；事件 ["a1","b1","c1"]
	r := mustRegistry(t, []ColumnSpec{
		{Name: "a", Default: "-"},
		{Name: "b", Default: "-"},
		{Name: "c", Default: "-"},
	})
	if _, err := r.DropColumn(1); err != nil { // 删除首列 a；当前为 b@0, c@1
		t.Fatal(err)
	}
	row, err := r.Decode(1, []string{"a1", "b1", "c1"})
	if err != nil {
		t.Fatal(err)
	}

	b, _ := cellByID(row, 2)
	c, _ := cellByID(row, 3)
	if b.Value != "b1" || c.Value != "c1" {
		t.Fatalf("id-based decode must keep each surviving column's own value: b=%q c=%q", b.Value, c.Value)
	}
	// 按位置映射会让“当前位置0的列(b)”错误地拿到事件位置0的值("a1"，已删除列 a 的值)。
	naivePositionalForCurrentPos0 := []string{"a1", "b1", "c1"}[0]
	if row.Cells()[0].Value == naivePositionalForCurrentPos0 {
		t.Fatalf("positional mapping corrupts b with deleted a's value %q; correct is %q",
			naivePositionalForCurrentPos0, row.Cells()[0].Value)
	}

	// 场景二：朴素“按名字映射”无法跨改名取值。
	// v1: a(id1), b(id2)；事件按 v1 名字建映射 {"a":"a1","b":"b1"}。
	r2 := mustRegistry(t, []ColumnSpec{{Name: "a", Default: "DA"}, {Name: "b", Default: "DB"}})
	if _, err := r2.RenameColumn(1, "alpha"); err != nil {
		t.Fatal(err)
	}
	eventByName := map[string]string{"a": "a1", "b": "b1"}
	if _, nameHit := eventByName["alpha"]; nameHit {
		t.Fatal("naive name mapping unexpectedly finds renamed column 'alpha' in v1 event")
	}
	row2, err := r2.Decode(1, []string{"a1", "b1"})
	if err != nil {
		t.Fatal(err)
	}
	alpha, _ := cellByID(row2, 1)
	if alpha.Name != "alpha" || alpha.Value != "a1" {
		t.Fatalf("id-based decode follows the rename: id1 must be alpha=a1, got %+v", alpha)
	}
}

// ---- 解码的各类非法输入 --------------------------------------------------------

func TestDecode_InvalidInputs(t *testing.T) {
	r := mustRegistry(t, []ColumnSpec{{Name: "a", Default: "DA"}, {Name: "b", Default: "DB"}})

	for _, v := range []int{0, -1, 2, 99} {
		_, err := r.Decode(v, []string{"x", "y"})
		if !errors.Is(err, ErrVersionNotFound) {
			t.Fatalf("version %d: expected ErrVersionNotFound, got %v", v, err)
		}
	}

	for _, vals := range [][]string{{"only-one"}, {"x", "y", "too-many"}, nil} {
		_, err := r.Decode(1, vals)
		if !errors.Is(err, ErrValueCountMismatch) {
			t.Fatalf("values %v: expected ErrValueCountMismatch, got %v", vals, err)
		}
	}

	// 非法解码不得改变版本列表。
	if r.VersionCount() != 1 {
		t.Fatalf("failed decodes mutated version count: %d", r.VersionCount())
	}
}

// ---- 非法演进被拒绝且不改变版本列表与标识分配 -------------------------------------

func TestInvalidEvolution_RejectedWithoutStateChange(t *testing.T) {
	r := mustRegistry(t, []ColumnSpec{{Name: "a", Default: "DA"}, {Name: "b", Default: "DB"}})

	badOps := []func() error{
		func() error { _, e := r.AddColumn("", "D"); return e },
		func() error { _, e := r.AddColumn("a", "D"); return e },    // 重名
		func() error { _, e := r.DropColumn(999); return e },        // 不存在的标识
		func() error { _, e := r.RenameColumn(999, "x"); return e }, // 不存在的标识
		func() error { _, e := r.RenameColumn(1, ""); return e },    // 空名字
		func() error { _, e := r.RenameColumn(1, "b"); return e },   // 与他列冲突
	}
	for i, op := range badOps {
		if err := op(); !errors.Is(err, ErrInvalidEvolution) {
			t.Fatalf("bad op %d: expected ErrInvalidEvolution, got %v", i, err)
		}
		if r.VersionCount() != 1 {
			t.Fatalf("bad op %d changed version count to %d", i, r.VersionCount())
		}
	}

	// 被拒的新增不得消耗标识：紧接着成功新增的列必须拿到 id=3（而不是跳过）。
	s, err := r.AddColumn("c", "DC")
	if err != nil {
		t.Fatal(err)
	}
	if id := s.Columns()[2].ID; id != 3 {
		t.Fatalf("rejected add must not consume id; expected 3 got %d", id)
	}

	// 单列表中拒绝删除唯一列。
	solo := mustRegistry(t, []ColumnSpec{{Name: "only", Default: "D"}})
	if _, err := solo.DropColumn(1); !errors.Is(err, ErrInvalidEvolution) {
		t.Fatalf("dropping last column: expected ErrInvalidEvolution, got %v", err)
	}
	if solo.VersionCount() != 1 {
		t.Fatal("dropping last column must not publish a version")
	}

	// 改名成当前名字视为无操作，不发布新版本。
	if _, err := r.RenameColumn(1, "a"); err != nil {
		t.Fatalf("rename to same name should be a no-op, got %v", err)
	}
	if r.VersionCount() != 2 {
		t.Fatalf("same-name rename must not publish a version, count=%d", r.VersionCount())
	}
}

// ---- 版本数超限 ----------------------------------------------------------------

func TestVersionLimitExceeded(t *testing.T) {
	r := mustRegistry(t, []ColumnSpec{{Name: "a", Default: "DA"}}, WithMaxVersions(2))
	if _, err := r.AddColumn("b", "DB"); err != nil {
		t.Fatalf("evolution up to the limit should succeed: %v", err)
	}
	for i := 0; i < 3; i++ {
		_, err := r.AddColumn(fmt.Sprintf("x%d", i), "DX")
		if !errors.Is(err, ErrVersionLimitExceeded) {
			t.Fatalf("attempt %d: expected ErrVersionLimitExceeded, got %v", i, err)
		}
		if r.VersionCount() != 2 {
			t.Fatalf("rejected evolution must not add versions, count=%d", r.VersionCount())
		}
	}

	// 超限拒绝同样不得消耗标识；改名也会发布新版本，因此一样被限流。
	_, err := r.RenameColumn(1, "a2")
	if !errors.Is(err, ErrVersionLimitExceeded) {
		t.Fatalf("rename at limit must be rejected, got %v", err)
	}
}

// ---- 批量解码：任一被拒整批失败，不返回部分结果 -------------------------------------

func TestDecodeBatch_Atomicity(t *testing.T) {
	r := mustRegistry(t, []ColumnSpec{{Name: "a", Default: "DA"}})
	if _, err := r.AddColumn("b", "DB"); err != nil {
		t.Fatal(err)
	}

	rows, err := r.DecodeBatch([]Event{
		{Version: 1, Values: []string{"a1"}},
		{Version: 2, Values: []string{"a2", "b2"}},
		{Version: 1, Values: []string{"a3"}},
	})
	if err != nil {
		t.Fatalf("valid batch: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if v := rows[0].Cells()[0]; v.Value != "a1" || !rows[0].Cells()[1].FromDefault {
		t.Fatalf("row 0 wrong: %+v", rows[0].Cells())
	}
	if v := rows[1].Cells()[1]; v.FromDefault || v.Value != "b2" {
		t.Fatalf("row 1 wrong: %+v", rows[1].Cells())
	}

	// 中间事件非法：整批必须返回 nil，绝不能给出前两条的部分结果。
	for _, bad := range []Event{
		{Version: 99, Values: []string{"x"}},
		{Version: 1, Values: []string{"x", "y"}}, // 值个数不符
	} {
		rows, err := r.DecodeBatch([]Event{
			{Version: 1, Values: []string{"ok1"}},
			bad,
			{Version: 1, Values: []string{"ok2"}},
		})
		if rows != nil {
			t.Fatalf("batch must return no partial rows, got %d", len(rows))
		}
		if bad.Version == 99 && !errors.Is(err, ErrVersionNotFound) {
			t.Fatalf("want version not found, got %v", err)
		}
		if bad.Version == 1 && !errors.Is(err, ErrValueCountMismatch) {
			t.Fatalf("want count mismatch, got %v", err)
		}
	}

	// 空批量返回空结果且无错误。
	rows, err = r.DecodeBatch(nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("empty batch: rows=%v err=%v", rows, err)
	}
}

// ---- 确定性：同一输入反复计算输出完全相同 ------------------------------------------

func TestDecode_Deterministic(t *testing.T) {
	r := mustRegistry(t, []ColumnSpec{{Name: "a", Default: "DA"}, {Name: "b", Default: "DB"}})
	if _, err := r.AddColumn("c", "DC"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.DropColumn(2); err != nil {
		t.Fatal(err)
	}

	input := []string{"", "b-val"}
	first, err := r.Decode(1, input)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		got, err := r.Decode(1, input)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprintf("%v", got.Cells()) != fmt.Sprintf("%v", first.Cells()) ||
			got.SchemaVersion != first.SchemaVersion {
			t.Fatalf("iteration %d not deterministic:\nfirst=%v\ngot =%v", i, first.Cells(), got.Cells())
		}
	}
}

// ---- 并发：解码与演进并发，每个结果完整基于某一版本 ---------------------------------

func TestConcurrent_DecodeAndEvolution(t *testing.T) {
	specs := make([]ColumnSpec, 5)
	v1IDs := map[ColumnID]bool{}
	for i := range specs {
		specs[i] = ColumnSpec{Name: fmt.Sprintf("c%d", i), Default: "D"}
		v1IDs[ColumnID(i+1)] = true
	}
	r := mustRegistry(t, specs)

	const writers = 8
	const addsPerWriter = 25
	var wg sync.WaitGroup

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < addsPerWriter; i++ {
				if _, err := r.AddColumn(fmt.Sprintf("w%d_c%d", w, i), "D"); err != nil {
					t.Errorf("concurrent add: %v", err)
					return
				}
			}
		}(w)
	}

	const decoders = 8
	for d := 0; d < decoders; d++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v1Vals := []string{"1", "2", "3", "4", "5"}
			for k := 0; k < 500; k++ {
				var decodeVersion int
				var vals []string
				isV1Event := k%2 == 0
				if isV1Event {
					// 永远合法的 v1 事件。
					decodeVersion, vals = 1, v1Vals
				} else {
					// 与钉住快照等宽的该版本事件。
					snap := r.Latest()
					decodeVersion = snap.Version()
					vals = make([]string, snap.width())
					for i, c := range snap.Columns() {
						vals[i] = strconv.FormatInt(int64(c.ID), 10)
					}
				}
				row, err := r.Decode(decodeVersion, vals)
				if err != nil {
					t.Errorf("concurrent decode: %v", err)
					return
				}
				// 结果必须完整自洽于其声明所基于的版本。
				basis, ok := r.SchemaAt(row.SchemaVersion)
				if !ok {
					t.Errorf("row references unknown version %d", row.SchemaVersion)
					return
				}
				cells := row.Cells()
				if len(cells) != basis.width() {
					t.Errorf("row width %d != schema v%d width %d", len(cells), basis.version, basis.width())
					return
				}
				for i, cell := range cells {
					col, _ := basis.columnAt(i)
					if col.ID != cell.ID || col.Name != cell.Name {
						t.Errorf("cell %d inconsistent with schema: cell=%+v col=%+v", i, cell, col)
						return
					}
					// v1 事件：原有 5 列必须携带事件值，后续新增列必须来自默认值。
					if isV1Event && v1IDs[cell.ID] {
						if cell.FromDefault || cell.Value != strconv.FormatInt(int64(cell.ID), 10) {
							t.Errorf("v1 event cell wrong: %+v", cell)
							return
						}
					}
					if isV1Event && !v1IDs[cell.ID] && !cell.FromDefault {
						t.Errorf("post-v1 column must be defaulted for v1 event: %+v", cell)
						return
					}
				}
			}
		}()
	}

	wg.Wait()

	if got, want := r.VersionCount(), 1+writers*addsPerWriter; got != want {
		t.Fatalf("version count = %d, want %d", got, want)
	}
	latest := r.Latest()
	if got, want := latest.width(), 5+writers*addsPerWriter; got != want {
		t.Fatalf("latest width = %d, want %d", got, want)
	}
	// 标识全局唯一、永不复用：本场景只有新增，最新结构应含全部已分配标识且无重复。
	idSeen := map[ColumnID]bool{}
	for _, c := range latest.Columns() {
		if idSeen[c.ID] {
			t.Fatalf("duplicate column id %d in latest schema", c.ID)
		}
		idSeen[c.ID] = true
	}
	if got, want := len(idSeen), 5+writers*addsPerWriter; got != want {
		t.Fatalf("allocated id count = %d, want %d (no reuse)", got, want)
	}
}

// ---- README 示例逐字校验 --------------------------------------------------------

func TestReadmeExample(t *testing.T) {
	r := mustRegistry(t, []ColumnSpec{
		{Name: "user", Default: "unknown"},
		{Name: "city", Default: "-"},
	})
	if _, err := r.AddColumn("email", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RenameColumn(1, "account"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.DropColumn(2); err != nil {
		t.Fatal(err)
	}
	readded, err := r.AddColumn("city", "unknown-city")
	if err != nil {
		t.Fatal(err)
	}

	row, err := r.Decode(1, []string{"alice", ""})
	if err != nil {
		t.Fatal(err)
	}

	account, _ := cellByID(row, 1)
	if account.Name != "account" || account.Value != "alice" || account.FromDefault {
		t.Fatalf("account(id=1): %+v", account)
	}
	email, _ := cellByID(row, 3)
	if email.Name != "email" || email.Value != "" || !email.FromDefault {
		t.Fatalf("email(id=3) missing column default: %+v", email)
	}
	var newCityID ColumnID
	for _, c := range readded.Columns() {
		if c.Name == "city" {
			newCityID = c.ID
		}
	}
	if newCityID != 4 {
		t.Fatalf("re-added city must get fresh id 4, got %d", newCityID)
	}
	newCity, _ := cellByID(row, newCityID)
	if newCity.Value != "unknown-city" || !newCity.FromDefault {
		t.Fatalf("re-added city(id=%d): %+v", newCityID, newCity)
	}
	if _, exists := row.Value(2); exists {
		t.Fatal("deleted id 2 must be absent from result")
	}
	if len(row.Cells()) != 3 {
		t.Fatalf("result must have exactly the 3 current columns, got %d", len(row.Cells()))
	}
}

// ---- 日志：打印输入、解码结果与判定依据 ------------------------------------------
func TestDecode_Logging(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	r := mustRegistry(t, []ColumnSpec{{Name: "a", Default: "DA"}}, WithLogger(logger))
	if _, err := r.AddColumn("b", "DB"); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	if _, err := r.Decode(1, []string{"ev1"}); err != nil {
		t.Fatal(err)
	}
	log := buf.String()
	for _, want := range []string{"decode accepted", "input_version=1", "input_values", "ev1",
		"result_schema_version=2", "result_cells", "basis"} {
		if !strings.Contains(log, want) {
			t.Fatalf("decode log missing %q\nlog:\n%s", want, log)
		}
	}

	buf.Reset()
	if _, err := r.Decode(7, []string{"x"}); err == nil {
		t.Fatal("expected rejection")
	}
	rejectLog := buf.String()
	for _, want := range []string{"decode rejected", "reason=version_not_found", "input_version=7"} {
		if !strings.Contains(rejectLog, want) {
			t.Fatalf("rejection log missing %q\nlog:\n%s", want, rejectLog)
		}
	}
}
