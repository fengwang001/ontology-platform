package access

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"ontology/mask"
	"ontology/policy"
)

func i64(v int) policy.Cell { return int64(v) }

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// exampleStore builds the running example from the specification.
func exampleStore(t testing.TB) *policy.Store {
	t.Helper()
	s := policy.NewStore()
	must(t, s.AddTable("t", []policy.Column{
		{Name: "id", Type: policy.TypeInt, Def: 0},
		{Name: "ssn", Type: policy.TypeStr, Def: 3},
		{Name: "sal", Type: policy.TypeInt, Def: 2},
		{Name: "dept", Type: policy.TypeStr, Def: 0},
	}))
	must(t, s.Insert("t", []policy.Cell{i64(1), "123456789", i64(5250), "hr"}))
	must(t, s.Insert("t", []policy.Cell{i64(2), "987", i64(-250), "it"}))
	must(t, s.Insert("t", []policy.Cell{i64(3), nil, i64(7000), "hr"}))
	must(t, s.AddRowPolicy(policy.RowPolicy{
		ID: "P1", Table: "t", Role: "analyst", Kind: policy.Permissive,
		Pred: []policy.Atom{{Col: "dept", Op: policy.OpEq, Constant: "hr"}},
	}))
	must(t, s.AddRowPolicy(policy.RowPolicy{
		ID: "P2", Table: "t", Role: "auditor", Kind: policy.Permissive,
	}))
	must(t, s.AddRowPolicy(policy.RowPolicy{
		ID: "R1", Table: "t", Role: "*", Kind: policy.Restrictive,
		Pred: []policy.Atom{{Col: "sal", Op: policy.OpLe, Constant: i64(6000)}},
	}))
	must(t, s.SetMask(policy.MaskRule{Table: "t", Col: "ssn", Role: "analyst", Level: 1}))
	must(t, s.SetMask(policy.MaskRule{Table: "t", Col: "sal", Role: "analyst", Level: 1}))
	must(t, s.SetMask(policy.MaskRule{Table: "t", Col: "sal", Role: "auditor", Level: 3}))
	return s
}

// TestExample walks every scenario given in the specification and logs the
// input, output and decision basis.
func TestExample(t *testing.T) {
	r := NewReader(exampleStore(t))

	res, err := r.Read([]string{"analyst"}, "t", []string{"ssn", "sal"}, nil, 10)
	must(t, err)
	want := []ResultRow{{RowNo: 1, Cells: []policy.Cell{"*****6789", int64(5200)}}}
	if !reflect.DeepEqual(res.Rows, want) || res.Truncated {
		t.Fatalf("analyst rows = %#v truncated=%v, want %#v", res.Rows, res.Truncated, want)
	}
	t.Logf("IN roles={analyst} select=[ssn,sal] | OUT=%v | BASIS: P1(dept=hr) hits row1; R1(sal<=6000) plaintext; row3 NULL sal? no-7000 fails R1", res.Rows)

	res, err = r.Read([]string{"analyst"}, "t", []string{"id"},
		[]policy.Atom{{Col: "sal", Op: policy.OpEq, Constant: i64(5250)}}, 10)
	must(t, err)
	if len(res.Rows) != 0 {
		t.Fatalf("sal=5250 on masked: got %d rows, want 0", len(res.Rows))
	}
	res, err = r.Read([]string{"analyst"}, "t", []string{"id"},
		[]policy.Atom{{Col: "sal", Op: policy.OpEq, Constant: i64(5200)}}, 10)
	must(t, err)
	if len(res.Rows) != 1 || res.Rows[0].RowNo != 1 {
		t.Fatalf("sal=5200 on masked: got %+v, want row 1", res.Rows)
	}
	t.Logf("IN where sal=5250 | OUT=0 rows; IN where sal=5200 | OUT=row1 | BASIS: where evaluates masked values")

	res, err = r.Read([]string{"analyst", "auditor"}, "t", []string{"ssn", "sal"}, nil, 10)
	must(t, err)
	want = []ResultRow{
		{RowNo: 1, Cells: []policy.Cell{"*****6789", int64(5200)}},
		{RowNo: 2, Cells: []policy.Cell{"***", int64(-300)}},
	}
	if !reflect.DeepEqual(res.Rows, want) {
		t.Fatalf("both roles = %#v, want %#v", res.Rows, want)
	}
	t.Logf("IN roles={analyst,auditor} | OUT=%v | BASIS: P1 or P2; level=min(analyst1,auditor3)=1; -250 floors to -300", res.Rows)

	res, err = r.Read([]string{"auditor"}, "t", []string{"ssn", "sal"},
		[]policy.Atom{{Col: "sal", Op: policy.OpLe, Constant: i64(6000)}}, 10)
	must(t, err)
	if len(res.Rows) != 0 {
		t.Fatalf("auditor sal<=6000: got %d rows, want 0 (NULL atom false)", len(res.Rows))
	}
	res, err = r.Read([]string{"auditor"}, "t", []string{"ssn", "sal"}, nil, 10)
	must(t, err)
	if len(res.Rows) != 2 || res.Rows[0].Cells[0] != nil || res.Rows[0].Cells[1] != nil {
		t.Fatalf("auditor levels: got %#v, want two rows of NULLs", res.Rows)
	}
	t.Logf("IN roles={auditor} | OUT=two NULL rows; where sal<=6000 -> 0 rows | BASIS: level 3 NULL makes atoms false")

	res, err = r.Read([]string{"intern"}, "t", []string{"id"}, nil, 10)
	must(t, err)
	if len(res.Rows) != 0 {
		t.Fatalf("intern default deny: got %d rows, want 0", len(res.Rows))
	}
	t.Logf("IN roles={intern} | OUT=0 rows, no error | BASIS: only R1 applies, no permissive -> default deny")
}

// TestStringLengths covers str partial at the exact 4/5 byte boundary.
func TestStringLengths(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""}, {"a", "*"}, {"abcd", "****"}, {"abcde", "*bcde"}, {"123456789", "*****6789"},
	}
	for _, c := range cases {
		if got := mask.Apply(c.in, policy.TypeStr, mask.Partial); got != c.want {
			t.Errorf("partial(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFloorRounding checks negative-infinity rounding around zero.
func TestFloorRounding(t *testing.T) {
	cases := []struct{ in, want int }{
		{5250, 5200}, {5200, 5200}, {99, 0}, {-1, -100}, {-250, -300}, {-300, -300},
	}
	for _, c := range cases {
		if got := mask.Apply(i64(c.in), policy.TypeInt, mask.Partial); got != int64(c.want) {
			t.Errorf("floor(%d) = %v, want %d", c.in, got, c.want)
		}
	}
}

// TestHash verifies FNV-1a 64 hex for str and int (int uses decimal text) and
// the int->str type change at level 2.
func TestHash(t *testing.T) {
	fnvHex := func(b []byte) string {
		h := fnv.New64a()
		h.Write(b)
		return fmt.Sprintf("%016x", h.Sum64())
	}
	if got := mask.Apply("abc", policy.TypeStr, mask.Hashed); got != fnvHex([]byte("abc")) {
		t.Fatalf("hash str = %v", got)
	}
	if got := mask.Apply(i64(-250), policy.TypeInt, mask.Hashed); got != fnvHex([]byte("-250")) {
		t.Fatalf("hash int = %v", got)
	}

	s := policy.NewStore()
	must(t, s.AddTable("t", []policy.Column{{Name: "x", Type: policy.TypeInt, Def: 2}}))
	must(t, s.Insert("t", []policy.Cell{i64(7)}))
	must(t, s.AddRowPolicy(policy.RowPolicy{ID: "p", Table: "t", Role: "*", Kind: policy.Permissive}))
	r := NewReader(s)
	if _, err := r.Read([]string{"u"}, "t", []string{"x"},
		[]policy.Atom{{Col: "x", Op: policy.OpEq, Constant: i64(7)}}, 10); !errors.Is(err, policy.ErrTypeMismatch) {
		t.Fatalf("hashed int = int constant: err=%v, want type mismatch", err)
	}
	if _, err := r.Read([]string{"u"}, "t", []string{"x"},
		[]policy.Atom{{Col: "x", Op: policy.OpLe, Constant: fnvHex([]byte("7"))}}, 10); !errors.Is(err, policy.ErrTypeMismatch) {
		t.Fatalf("hashed int with <=: err=%v, want type mismatch", err)
	}
	res, err := r.Read([]string{"u"}, "t", []string{"x"},
		[]policy.Atom{{Col: "x", Op: policy.OpEq, Constant: fnvHex([]byte("7"))}}, 10)
	must(t, err)
	if len(res.Rows) != 1 || res.Rows[0].Cells[0] != fnvHex([]byte("7")) {
		t.Fatalf("hashed read: %#v", res.Rows)
	}
}

// TestNullSemantics: NULL survives levels 0..3, and a restrictive != on NULL
// is false.
func TestNullSemantics(t *testing.T) {
	for level := 0; level <= 3; level++ {
		if got := mask.Apply(nil, policy.TypeStr, level); got != nil {
			t.Fatalf("NULL level %d -> %v", level, got)
		}
	}
	s := policy.NewStore()
	must(t, s.AddTable("t", []policy.Column{
		{Name: "id", Type: policy.TypeInt, Def: 0},
		{Name: "note", Type: policy.TypeStr, Def: 0},
	}))
	must(t, s.Insert("t", []policy.Cell{i64(1), nil}))
	must(t, s.AddRowPolicy(policy.RowPolicy{ID: "P", Table: "t", Role: "*", Kind: policy.Permissive}))
	must(t, s.AddRowPolicy(policy.RowPolicy{
		ID: "R", Table: "t", Role: "*", Kind: policy.Restrictive,
		Pred: []policy.Atom{{Col: "note", Op: policy.OpNe, Constant: "x"}},
	}))
	r := NewReader(s)
	res, err := r.Read([]string{"u"}, "t", []string{"id"}, nil, 10)
	must(t, err)
	if len(res.Rows) != 0 {
		t.Fatalf("restrictive != on NULL: got %d rows, want 0", len(res.Rows))
	}
}

// TestDefaultRoleParticipates: a role without a rule participates at def.
func TestDefaultRoleParticipates(t *testing.T) {
	s := policy.NewStore()
	must(t, s.AddTable("t", []policy.Column{{Name: "s", Type: policy.TypeStr, Def: 3}}))
	must(t, s.Insert("t", []policy.Cell{"secret"}))
	must(t, s.AddRowPolicy(policy.RowPolicy{ID: "P", Table: "t", Role: "*", Kind: policy.Permissive}))
	must(t, s.SetMask(policy.MaskRule{Table: "t", Col: "s", Role: "vip", Level: 0}))
	must(t, s.SetMask(policy.MaskRule{Table: "t", Col: "s", Role: "stricter", Level: 4}))
	r := NewReader(s)

	res, err := r.Read([]string{"vip"}, "t", []string{"s"}, nil, 10)
	must(t, err)
	if res.Rows[0].Cells[0] != "secret" {
		t.Fatalf("vip level 0: %v", res.Rows[0].Cells[0])
	}
	res, err = r.Read([]string{"vip", "newbie"}, "t", []string{"s"}, nil, 10)
	must(t, err)
	if res.Rows[0].Cells[0] != "secret" {
		t.Fatalf("min(0,def3) should stay plaintext, got %v", res.Rows[0].Cells[0])
	}
	res, err = r.Read([]string{"newbie"}, "t", []string{"s"}, nil, 10)
	must(t, err)
	if res.Rows[0].Cells[0] != nil {
		t.Fatalf("newbie def 3: %v, want NULL", res.Rows[0].Cells[0])
	}
	// {newbie, stricter}: min(def3, rule4) = 3 -> NULL, not denial.
	res, err = r.Read([]string{"newbie", "stricter"}, "t", []string{"s"}, nil, 10)
	must(t, err)
	if res.Rows[0].Cells[0] != nil {
		t.Fatalf("min(3,4)=3: %v, want NULL", res.Rows[0].Cells[0])
	}
	// Give newbie an explicit 4 as well: min(4,4)=4 -> denial.
	must(t, s.SetMask(policy.MaskRule{Table: "t", Col: "s", Role: "newbie", Level: 4}))
	_, err = r.Read([]string{"newbie", "stricter"}, "t", []string{"s"}, nil, 10)
	if !errors.Is(err, policy.ErrColumnDenied) {
		t.Fatalf("min(3,4)=4: err=%v, want denied", err)
	}
}

// TestReadRejectionOrder verifies "first error only" precedence:
// args > table > column > denied (select order then where order) > type.
func TestReadRejectionOrder(t *testing.T) {
	s := policy.NewStore()
	must(t, s.AddTable("t", []policy.Column{
		{Name: "a", Type: policy.TypeInt, Def: 4},
		{Name: "b", Type: policy.TypeInt, Def: 4},
		{Name: "c", Type: policy.TypeStr, Def: 2},
	}))
	must(t, s.Insert("t", []policy.Cell{i64(1), i64(2), "x"}))
	r := NewReader(s)

	// Invalid arguments beat everything else (bad role size, limit, dup col).
	_, err := r.Read([]string{}, "t", []string{"a"}, nil, 10)
	if !errors.Is(err, policy.ErrInvalidArgument) {
		t.Fatalf("empty roles: %v", err)
	}
	_, err = r.Read([]string{"u"}, "t", []string{"a", "a"}, nil, 10)
	if !errors.Is(err, policy.ErrInvalidArgument) {
		t.Fatalf("dup select cols: %v", err)
	}
	_, err = r.Read([]string{"u"}, "t", []string{"a"}, nil, 0)
	if !errors.Is(err, policy.ErrInvalidArgument) {
		t.Fatalf("limit 0: %v", err)
	}
	// A constant of the wrong dynamic type is a where type mismatch (it is a
	// legal Cell shape; constant/column types are compared post-denial order).
	_, err = r.Read([]string{"u"}, "t", []string{"c"}, []policy.Atom{{Col: "c", Op: policy.OpEq, Constant: i64(1)}}, 10)
	if !errors.Is(err, policy.ErrTypeMismatch) {
		t.Fatalf("int constant vs str column: %v", err)
	}
	// A non int64/string constant is an invalid argument.
	_, err = r.Read([]string{"u"}, "t", []string{"c"}, []policy.Atom{{Col: "c", Op: policy.OpEq, Constant: 1.5}}, 10)
	if !errors.Is(err, policy.ErrInvalidArgument) {
		t.Fatalf("float64 constant: %v, want invalid argument", err)
	}
	// Missing table beats missing columns and denial.
	_, err = r.Read([]string{"u"}, "nope", []string{"ghost"}, nil, 10)
	if !errors.Is(err, policy.ErrTableNotFound) {
		t.Fatalf("missing table: %v", err)
	}
	// Column not found beats denial of an existing column.
	_, err = r.Read([]string{"u"}, "t", []string{"ghost"}, nil, 10)
	if !errors.Is(err, policy.ErrColumnNotFound) {
		t.Fatalf("missing column: %v", err)
	}
	// Denied select column reported before denied/type problems in where.
	_, err = r.Read([]string{"u"}, "t", []string{"a"},
		[]policy.Atom{{Col: "b", Op: policy.OpEq, Constant: i64(2)}}, 10)
	if !errors.Is(err, policy.ErrColumnDenied) {
		t.Fatalf("denied select a: %v", err)
	}
	// Where denial: b (first atom) reported before a type error on c... build
	// select only allowed columns; where atoms order: b denied first.
	_, err = r.Read([]string{"c"}, "t", []string{"c"},
		[]policy.Atom{
			{Col: "b", Op: policy.OpEq, Constant: i64(2)},
			{Col: "c", Op: policy.OpLt, Constant: "x"},
		}, 10)
	if !errors.Is(err, policy.ErrColumnDenied) {
		t.Fatalf("where denial before type: %v", err)
	}
	// Type mismatch finally surfaces when nothing is denied/missing.
	_, err = r.Read([]string{"u"}, "t", []string{"c"},
		[]policy.Atom{{Col: "c", Op: policy.OpEq, Constant: i64(1)}}, 10)
	if !errors.Is(err, policy.ErrTypeMismatch) {
		t.Fatalf("int constant vs str column: %v", err)
	}
	// < on a post-mask str column is a type mismatch.
	_, err = r.Read([]string{"u"}, "t", []string{"c"},
		[]policy.Atom{{Col: "c", Op: policy.OpLt, Constant: "x"}}, 10)
	if !errors.Is(err, policy.ErrTypeMismatch) {
		t.Fatalf("< on str: %v", err)
	}
}

// TestLimitTruncated checks exact-limit boundary and row-number ordering.
func TestLimitTruncated(t *testing.T) {
	s := policy.NewStore()
	must(t, s.AddTable("t", []policy.Column{{Name: "id", Type: policy.TypeInt, Def: 0}}))
	for i := 1; i <= 5; i++ {
		must(t, s.Insert("t", []policy.Cell{i64(i)}))
	}
	must(t, s.AddRowPolicy(policy.RowPolicy{ID: "P", Table: "t", Role: "*", Kind: policy.Permissive}))
	r := NewReader(s)

	res, err := r.Read([]string{"u"}, "t", []string{"id"}, nil, 5)
	must(t, err)
	if len(res.Rows) != 5 || res.Truncated {
		t.Fatalf("limit==result: len=%d truncated=%v", len(res.Rows), res.Truncated)
	}
	if res.Rows[0].RowNo != 1 || res.Rows[4].RowNo != 5 {
		t.Fatalf("row numbers not ascending: %+v", res.Rows)
	}
	res, err = r.Read([]string{"u"}, "t", []string{"id"}, nil, 3)
	must(t, err)
	if len(res.Rows) != 3 || !res.Truncated {
		t.Fatalf("limit=3 of 5: len=%d truncated=%v", len(res.Rows), res.Truncated)
	}
	if res.Rows[2].Cells[0] != int64(3) {
		t.Fatalf("expected first 3 rows, last=%v", res.Rows[2].Cells[0])
	}
}

// TestEpoch counts accepted mutations: policies and masks bump, Insert and
// rejected calls do not.
func TestEpoch(t *testing.T) {
	s := policy.NewStore()
	if s.Epoch() != 0 {
		t.Fatalf("initial epoch = %d", s.Epoch())
	}
	must(t, s.AddTable("t", []policy.Column{{Name: "a", Type: policy.TypeInt, Def: 0}}))
	e1 := s.Epoch()
	must(t, s.Insert("t", []policy.Cell{i64(1)}))
	if s.Epoch() != e1 {
		t.Fatalf("Insert bumped epoch: %d -> %d", e1, s.Epoch())
	}
	must(t, s.AddRowPolicy(policy.RowPolicy{ID: "P", Table: "t", Role: "*", Kind: policy.Permissive}))
	if s.Epoch() != e1+1 {
		t.Fatalf("AddRowPolicy epoch = %d", s.Epoch())
	}
	must(t, s.DropRowPolicy("P"))
	if s.Epoch() != e1+2 {
		t.Fatalf("DropRowPolicy epoch = %d", s.Epoch())
	}
	must(t, s.SetMask(policy.MaskRule{Table: "t", Col: "a", Role: "u", Level: 2}))
	if s.Epoch() != e1+3 {
		t.Fatalf("SetMask epoch = %d", s.Epoch())
	}
	// Idempotent same-level set is not a change.
	must(t, s.SetMask(policy.MaskRule{Table: "t", Col: "a", Role: "u", Level: 2}))
	if s.Epoch() != e1+3 {
		t.Fatalf("same-level SetMask bumped epoch = %d", s.Epoch())
	}
	must(t, s.ClearMask("t", "a", "u"))
	if s.Epoch() != e1+4 {
		t.Fatalf("ClearMask epoch = %d", s.Epoch())
	}
	// Rejected mutations leave epoch untouched.
	bad := []error{
		s.AddTable("", nil),
		s.AddTable("t", []policy.Column{{Name: "a", Type: policy.TypeInt, Def: 0}}),
		s.Insert("nope", []policy.Cell{i64(1)}),
		s.AddRowPolicy(policy.RowPolicy{ID: "X", Table: "nope", Role: "*", Kind: policy.Permissive}),
		s.DropRowPolicy("ghost"),
		s.ClearMask("t", "a", "u"),
	}
	for i, err := range bad {
		if err == nil {
			t.Fatalf("rejected call %d returned nil", i)
		}
	}
	if s.Epoch() != e1+4 {
		t.Fatalf("rejected mutations changed epoch = %d", s.Epoch())
	}
}

// TestMutationErrorOrder covers args > table/column > exists/not-exists.
func TestMutationErrorOrder(t *testing.T) {
	s := policy.NewStore()
	must(t, s.AddTable("t", []policy.Column{
		{Name: "a", Type: policy.TypeInt, Def: 0},
		{Name: "s", Type: policy.TypeStr, Def: 0},
	}))
	check := func(err error, target error, msg string) {
		t.Helper()
		if !errors.Is(err, target) {
			t.Fatalf("%s: got %v, want %v", msg, err, target)
		}
	}
	check(s.AddTable("t2", nil), policy.ErrInvalidArgument, "zero columns")
	check(s.AddTable("t2", []policy.Column{{Name: "x", Type: 9, Def: 0}}), policy.ErrInvalidArgument, "bad type")
	check(s.AddTable("t2", []policy.Column{{Name: "x", Type: policy.TypeInt, Def: 9}}), policy.ErrInvalidArgument, "bad def")
	check(s.AddTable("t2", []policy.Column{{Name: "x", Type: policy.TypeInt, Def: 0}, {Name: "x", Type: policy.TypeInt, Def: 0}}), policy.ErrInvalidArgument, "dup cols")
	check(s.AddTable("t", []policy.Column{{Name: "x", Type: policy.TypeInt, Def: 0}}), policy.ErrAlreadyExists, "dup table")
	check(s.Insert("nope", []policy.Cell{i64(1)}), policy.ErrTableNotFound, "missing table")
	check(s.Insert("t", []policy.Cell{"x"}), policy.ErrInvalidArgument, "wrong cell type")
	check(s.AddRowPolicy(policy.RowPolicy{ID: "p", Table: "nope", Role: "*", Kind: policy.Permissive}), policy.ErrTableNotFound, "policy missing table")
	check(s.AddRowPolicy(policy.RowPolicy{ID: "p", Table: "t", Role: "*", Kind: policy.Permissive,
		Pred: []policy.Atom{{Col: "ghost", Op: policy.OpEq, Constant: i64(1)}}}), policy.ErrColumnNotFound, "policy missing col")
	check(s.AddRowPolicy(policy.RowPolicy{ID: "p", Table: "t", Role: "*", Kind: policy.Permissive,
		Pred: []policy.Atom{{Col: "s", Op: policy.OpEq, Constant: i64(1)}}}), policy.ErrInvalidArgument, "policy type mismatch")
	check(s.AddRowPolicy(policy.RowPolicy{ID: "p", Table: "t", Role: "*", Kind: policy.Permissive,
		Pred: []policy.Atom{{Col: "s", Op: policy.OpLt, Constant: "s1"}}}), policy.ErrInvalidArgument, "policy < on str")
	must(t, s.AddRowPolicy(policy.RowPolicy{ID: "p", Table: "t", Role: "*", Kind: policy.Permissive}))
	check(s.AddRowPolicy(policy.RowPolicy{ID: "p", Table: "t", Role: "*", Kind: policy.Permissive}), policy.ErrAlreadyExists, "dup policy id")
	check(s.SetMask(policy.MaskRule{Table: "nope", Col: "a", Role: "u", Level: 1}), policy.ErrTableNotFound, "mask missing table")
	check(s.SetMask(policy.MaskRule{Table: "t", Col: "ghost", Role: "u", Level: 1}), policy.ErrColumnNotFound, "mask missing col")
	check(s.SetMask(policy.MaskRule{Table: "t", Col: "a", Role: "u", Level: 9}), policy.ErrInvalidArgument, "bad level")
	check(s.ClearMask("nope", "a", "u"), policy.ErrTableNotFound, "clear missing table")
	check(s.ClearMask("t", "ghost", "u"), policy.ErrColumnNotFound, "clear missing col")
	check(s.ClearMask("t", "a", "u"), policy.ErrNotFound, "clear absent rule")
	check(s.DropRowPolicy(""), policy.ErrInvalidArgument, "drop bad id")
}

// TestTouchedBound builds the same read with 100 vs 10000 irrelevant rules
// (other tables and other roles) and asserts touched is identical and no
// greater than the applicable-rule count bound.
func TestTouchedBound(t *testing.T) {
	build := func(irrelevant int) (int, int, int) {
		s := policy.NewStore()
		must(t, s.AddTable("t", []policy.Column{
			{Name: "id", Type: policy.TypeInt, Def: 0},
			{Name: "s", Type: policy.TypeStr, Def: 1},
		}))
		must(t, s.Insert("t", []policy.Cell{i64(1), "x"}))
		// Applicable to subject {alice}: P(alice), P(*), R(alice); R(bob) not.
		must(t, s.AddRowPolicy(policy.RowPolicy{ID: "p1", Table: "t", Role: "alice", Kind: policy.Permissive}))
		must(t, s.AddRowPolicy(policy.RowPolicy{ID: "p2", Table: "t", Role: "*", Kind: policy.Permissive}))
		must(t, s.AddRowPolicy(policy.RowPolicy{ID: "r1", Table: "t", Role: "alice", Kind: policy.Restrictive}))
		must(t, s.AddRowPolicy(policy.RowPolicy{ID: "r2", Table: "t", Role: "bob", Kind: policy.Restrictive}))
		// Explicit rules for alice on the two columns.
		must(t, s.SetMask(policy.MaskRule{Table: "t", Col: "id", Role: "alice", Level: 0}))
		must(t, s.SetMask(policy.MaskRule{Table: "t", Col: "s", Role: "alice", Level: 2}))

		for i := 0; i < irrelevant; i++ {
			ot := fmt.Sprintf("other%d", i)
			must(t, s.AddTable(ot, []policy.Column{{Name: "z", Type: policy.TypeInt, Def: 0}}))
			must(t, s.AddRowPolicy(policy.RowPolicy{
				ID: fmt.Sprintf("op%d", i), Table: ot, Role: "alice", Kind: policy.Permissive,
			}))
			must(t, s.SetMask(policy.MaskRule{Table: ot, Col: "z", Role: "alice", Level: 1}))
			// Other-role rules on the target table must be ignored too.
			must(t, s.SetMask(policy.MaskRule{Table: "t", Col: "s", Role: fmt.Sprintf("bob%d", i), Level: 1}))
			must(t, s.AddRowPolicy(policy.RowPolicy{
				ID: fmt.Sprintf("tp%d", i), Table: "t", Role: fmt.Sprintf("bob%d", i), Kind: policy.Restrictive,
			}))
		}
		r := NewReader(s)
		// 3 applicable policies + mask rules for alice on id and s = 2.
		// where also touches s.
		res, err := r.Read([]string{"alice"}, "t", []string{"id", "s"},
			[]policy.Atom{{Col: "s", Op: policy.OpEq, Constant: strconv.FormatUint(0, 16) + strings.Repeat("0", 15)}}, 10)
		// Constant is an arbitrary str; equality likely false but read is legal.
		must(t, err)
		return res.touched, 3, 2
	}
	got100, pols, rules := build(100)
	got10000, _, _ := build(10000)
	if got100 != got10000 {
		t.Fatalf("touched changed with irrelevant rules: %d vs %d", got100, got10000)
	}
	if got100 > pols+rules {
		t.Fatalf("touched %d exceeds bound %d", got100, pols+rules)
	}
	if got100 != pols+rules {
		t.Fatalf("touched = %d, want exactly %d", got100, pols+rules)
	}
	t.Logf("touched=%d with 100 and 10000 irrelevant rules (bound policies+rules=%d)", got100, pols+rules)
}

// TestConcurrent exercises concurrent reads and mutations under -race.
func TestConcurrent(t *testing.T) {
	s := policy.NewStore()
	must(t, s.AddTable("t", []policy.Column{{Name: "id", Type: policy.TypeInt, Def: 0}}))
	must(t, s.Insert("t", []policy.Cell{i64(1)}))
	must(t, s.AddRowPolicy(policy.RowPolicy{ID: "P", Table: "t", Role: "*", Kind: policy.Permissive}))
	r := NewReader(s)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = r.Read([]string{"u"}, "t", []string{"id"}, nil, 10)
				if g == 0 && i%50 == 0 {
					_ = s.SetMask(policy.MaskRule{Table: "t", Col: "id", Role: "u", Level: (i / 50) % 4})
				}
			}
		}(g)
	}
	wg.Wait()
}

// ---- Naive row-by-row, rule-by-rule simulation for differential testing ----

type oraCol struct {
	typ policy.ColType
	def int
}

type oracle struct {
	cols   map[string]oraCol
	colSeq []string
	rows   map[string][][]policy.Cell
	pols   map[string][]policy.RowPolicy
	mrules map[string]map[[2]string]int // table -> (col,role) -> level
	epoch  int
}

func newOracle() *oracle {
	return &oracle{
		cols:   map[string]oraCol{},
		rows:   map[string][][]policy.Cell{},
		pols:   map[string][]policy.RowPolicy{},
		mrules: map[string]map[[2]string]int{},
	}
}

func oraMask(cell policy.Cell, ct policy.ColType, level int) policy.Cell {
	if cell == nil || level == 3 {
		return nil
	}
	switch level {
	case 0:
		return cell
	case 1:
		if ct == policy.TypeInt {
			v := cell.(int64)
			q := v / 100
			if v < 0 && v%100 != 0 {
				q--
			}
			return q * 100
		}
		b := []byte(cell.(string))
		if len(b) <= 4 {
			return strings.Repeat("*", len(b))
		}
		return strings.Repeat("*", len(b)-4) + string(b[len(b)-4:])
	case 2:
		var text []byte
		if sv, ok := cell.(string); ok {
			text = []byte(sv)
		} else {
			text = []byte(strconv.FormatInt(cell.(int64), 10))
		}
		h := fnv.New64a()
		h.Write(text)
		return fmt.Sprintf("%016x", h.Sum64())
	}
	return cell
}

func oraAtom(v policy.Cell, op policy.Op, c policy.Cell) bool {
	if v == nil {
		return false
	}
	switch op {
	case policy.OpEq:
		return reflect.DeepEqual(v, c)
	case policy.OpNe:
		return !reflect.DeepEqual(v, c)
	case policy.OpLt:
		return v.(int64) < c.(int64)
	case policy.OpLe:
		return v.(int64) <= c.(int64)
	}
	return false
}

func indexOf(xs []string, x string) int {
	for i := range xs {
		if xs[i] == x {
			return i
		}
	}
	return -1
}

// oraRead simulates the full read and independently recounts touched:
// applicable policies plus subject-role mask rules on touched columns.
func (o *oracle) read(roles []string, table string, sel []string, where []policy.Atom, limit int) (*ReadResult, int) {
	roleSet := map[string]bool{}
	for _, rn := range roles {
		roleSet[rn] = true
	}
	need := map[string]bool{}
	for _, c := range sel {
		need[c] = true
	}
	for _, a := range where {
		need[a.Col] = true
	}
	levels := map[string]int{}
	for c := range need {
		l := 5 // above the maximum; roles is never empty
		for _, rn := range roles {
			rl := o.cols[c].def
			if v, ok := o.mrules[table][[2]string{c, rn}]; ok {
				rl = v
			}
			if rl < l {
				l = rl
			}
		}
		levels[c] = l
	}

	touched := 0
	var applPols []policy.RowPolicy
	for _, p := range o.pols[table] {
		if p.Role == "*" || roleSet[p.Role] {
			applPols = append(applPols, p)
			touched++
		}
	}
	for c := range need {
		for _, rn := range roles {
			if _, ok := o.mrules[table][[2]string{c, rn}]; ok {
				touched++
			}
		}
	}

	var outRows []ResultRow
	trunc := false
rows:
	for rowIdx, row := range o.rows[table] {
		anyP := false
		for _, p := range applPols {
			hit := true
			for _, a := range p.Pred {
				if !oraAtom(row[indexOf(o.colSeq, a.Col)], a.Op, a.Constant) {
					hit = false
					break
				}
			}
			if p.Kind == policy.Permissive && hit {
				anyP = true
			}
			if p.Kind == policy.Restrictive && !hit {
				continue rows
			}
		}
		if !anyP {
			continue
		}
		masked := make([]policy.Cell, len(row))
		for i, cn := range o.colSeq {
			masked[i] = oraMask(row[i], o.cols[cn].typ, levels[cn])
		}
		for _, a := range where {
			if levels[a.Col] == 3 {
				continue rows
			}
			if !oraAtom(masked[indexOf(o.colSeq, a.Col)], a.Op, a.Constant) {
				continue rows
			}
		}
		if len(outRows) >= limit {
			trunc = true
			break
		}
		cells := make([]policy.Cell, len(sel))
		for i, cn := range sel {
			cells[i] = masked[indexOf(o.colSeq, cn)]
		}
		outRows = append(outRows, ResultRow{RowNo: rowIdx + 1, Cells: cells})
	}
	return &ReadResult{Rows: outRows, Truncated: trunc, Epoch: o.epoch}, touched
}

const diffCases = 1500

type diffDesc struct {
	roles []string
	sel   []string
	where []policy.Atom
	limit int
	basis string
}

func (d diffDesc) String() string {
	return fmt.Sprintf("roles=%v sel=%v where=%v limit=%d basis=%s", d.roles, d.sel, d.where, d.limit, d.basis)
}

func randStr(rng *rand.Rand, n int) string {
	const alpha = "ab"
	b := make([]byte, n)
	for i := range b {
		b[i] = alpha[rng.Intn(len(alpha))]
	}
	return string(b)
}

func runDifferentialCase(t *testing.T, seed int64, noise int) (*ReadResult, *oracle, int, diffDesc) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	s := policy.NewStore()
	ora := newOracle()

	ncol := 1 + rng.Intn(3)
	cols := make([]policy.Column, ncol)
	for i := range cols {
		name := fmt.Sprintf("c%d", i)
		ct := policy.TypeInt
		if rng.Intn(2) == 1 {
			ct = policy.TypeStr
		}
		def := rng.Intn(5)
		cols[i] = policy.Column{Name: name, Type: ct, Def: def}
		ora.cols[name] = oraCol{typ: ct, def: def}
		ora.colSeq = append(ora.colSeq, name)
	}
	must(t, s.AddTable("t", cols))
	ora.epoch++

	nrows := rng.Intn(6)
	for ri := 0; ri < nrows; ri++ {
		row := make([]policy.Cell, ncol)
		for i, c := range cols {
			if rng.Intn(4) == 0 {
				row[i] = nil
				continue
			}
			if c.Type == policy.TypeInt {
				row[i] = int64(rng.Intn(2000) - 1000)
			} else {
				row[i] = randStr(rng, rng.Intn(8))
			}
		}
		must(t, s.Insert("t", row))
		ora.rows["t"] = append(ora.rows["t"], row)
	}

	rolePool := []string{"a", "b", "c", "d"}
	np := rng.Intn(6)
	for pi := 0; pi < np; pi++ {
		role := rolePool[rng.Intn(len(rolePool))]
		if rng.Intn(4) == 0 {
			role = "*"
		}
		k := policy.Permissive
		if rng.Intn(2) == 0 {
			k = policy.Restrictive
		}
		var atoms []policy.Atom
		na := rng.Intn(4)
		for ai := 0; ai < na; ai++ {
			ci := rng.Intn(ncol)
			op := policy.Op(rng.Intn(4) + 1)
			if cols[ci].Type == policy.TypeStr && (op == policy.OpLt || op == policy.OpLe) {
				op = policy.OpEq
			}
			var con policy.Cell
			if cols[ci].Type == policy.TypeInt {
				con = int64(rng.Intn(2000) - 1000)
			} else {
				con = randStr(rng, rng.Intn(8))
			}
			atoms = append(atoms, policy.Atom{Col: cols[ci].Name, Op: op, Constant: con})
		}
		p := policy.RowPolicy{ID: fmt.Sprintf("pol%d", pi), Table: "t", Role: role, Kind: k, Pred: atoms}
		must(t, s.AddRowPolicy(p))
		ora.pols["t"] = append(ora.pols["t"], p)
		ora.epoch++
	}

	ora.mrules["t"] = map[[2]string]int{}
	nm := rng.Intn(6)
	for mi := 0; mi < nm; mi++ {
		ci := rng.Intn(ncol)
		role := rolePool[rng.Intn(len(rolePool))]
		key := [2]string{cols[ci].Name, role}
		if _, exists := ora.mrules["t"][key]; exists {
			continue
		}
		level := rng.Intn(5)
		mr := policy.MaskRule{Table: "t", Col: cols[ci].Name, Role: role, Level: level}
		must(t, s.SetMask(mr))
		ora.mrules["t"][key] = level
		ora.epoch++
	}

	nr := 1 + rng.Intn(3)
	perm := rng.Perm(len(rolePool))
	var roles []string
	for i := 0; i < nr; i++ {
		roles = append(roles, rolePool[perm[i]])
	}
	levelAt := func(cn string) int {
		l := 5 // above the maximum; roles is never empty
		for _, rn := range roles {
			rl := ora.cols[cn].def
			if v, ok := ora.mrules["t"][[2]string{cn, rn}]; ok {
				rl = v
			}
			if rl < l {
				l = rl
			}
		}
		return l
	}

	// Select columns avoiding level 4 so the read is accepted; keep distinct.
	var pick []int
	for _, i := range rng.Perm(ncol) {
		if levelAt(cols[i].Name) != 4 {
			pick = append(pick, i)
		}
		if len(pick) == 1+rng.Intn(ncol) {
			break
		}
	}
	if len(pick) == 0 {
		// All columns denied: select the first one and expect a denial.
		pick = []int{0}
	}
	sel := make([]string, len(pick))
	for i, ci := range pick {
		sel[i] = cols[ci].Name
	}

	// Where atoms: build legal ones against post-mask types, occasionally
	// inject an intentional type error.
	var where []policy.Atom
	nw := rng.Intn(3)
	for wi := 0; wi < nw; wi++ {
		ci := pick[rng.Intn(len(pick))]
		cn := cols[ci].Name
		lv := levelAt(cn)
		if lv == 4 {
			break
		}
		postType := cols[ci].Type
		if lv == 2 {
			postType = policy.TypeStr
		}
		op := policy.OpEq
		if postType == policy.TypeInt && rng.Intn(4) >= 2 {
			op = []policy.Op{policy.OpNe, policy.OpLt, policy.OpLe}[rng.Intn(3)]
		} else if rng.Intn(2) == 1 {
			op = policy.OpNe
		}
		var con policy.Cell
		if rng.Intn(3) == 0 && len(ora.rows["t"]) > 0 {
			// Bias toward an actual masked value.
			row := ora.rows["t"][rng.Intn(len(ora.rows["t"]))]
			mv := oraMask(row[ci], cols[ci].Type, lv)
			if mv != nil {
				con = mv
			}
		}
		if con == nil {
			if postType == policy.TypeInt {
				con = int64(rng.Intn(2000) - 1000)
			} else {
				con = randStr(rng, 1+rng.Intn(16))
			}
		}
		where = append(where, policy.Atom{Col: cn, Op: op, Constant: con})
	}
	limit := 1 + rng.Intn(4)

	// Irrelevant noise: other tables and other-role policies/masks on "t".
	for i := 0; i < noise; i++ {
		ot := fmt.Sprintf("noise%d", i)
		must(t, s.AddTable(ot, []policy.Column{{Name: "z", Type: policy.TypeInt, Def: 0}}))
		must(t, s.AddRowPolicy(policy.RowPolicy{
			ID: fmt.Sprintf("npol%d", i), Table: ot, Role: "a", Kind: policy.Permissive,
		}))
		must(t, s.SetMask(policy.MaskRule{Table: ot, Col: "z", Role: "a", Level: 1}))
		extraRole := fmt.Sprintf("z%d", i)
		must(t, s.AddRowPolicy(policy.RowPolicy{
			ID: fmt.Sprintf("tpoln%d", i), Table: "t", Role: extraRole, Kind: policy.Restrictive,
		}))
		must(t, s.SetMask(policy.MaskRule{Table: "t", Col: cols[0].Name, Role: extraRole, Level: 1}))
	}

	r := NewReader(s)
	res, err := r.Read(roles, "t", sel, where, limit)
	if err != nil && levelAt(sel[0]) != 4 {
		levels := map[string]int{}
		for _, c := range cols {
			levels[c.Name] = levelAt(c.Name)
		}
		t.Fatalf("seed=%d unexpected read error: %v levels=%v roles=%v sel=%v where=%v", seed, err, levels, roles, sel, where)
	}
	if levelAt(sel[0]) == 4 {
		if !errors.Is(err, policy.ErrColumnDenied) {
			t.Fatalf("seed=%d expected denied, got %v", seed, err)
		}
		_, touched := ora.read(roles, "t", sel, where, limit)
		return &ReadResult{}, ora, touched, diffDesc{roles: roles, sel: sel, where: where, limit: limit, basis: "expected denial path; oracle used for touched baseline"}
	}
	must(t, err)
	return res, ora, res.touched, diffDesc{
		roles: roles, sel: sel, where: where, limit: limit,
		basis: "plaintext policies; where on post-mask cells; level=min role incl. defaults",
	}
}

// TestRandomDifferential runs 1500 random scenarios against the naive oracle.
func TestRandomDifferential(t *testing.T) {
	for iter := 0; iter < diffCases; iter++ {
		seed := int64(1000 + iter)
		out, ora, touched, desc := runDifferentialCase(t, seed, 0)
		if touched < 0 {
			t.Fatal("impossible")
		}
		if !strings.Contains(desc.basis, "denial") {
			wantRes, wantTouched := ora.read(desc.roles, "t", desc.sel, desc.where, desc.limit)
			if !reflect.DeepEqual(out.Rows, wantRes.Rows) || out.Truncated != wantRes.Truncated || out.Epoch != wantRes.Epoch {
				t.Fatalf("seed=%d\nPROD=%#v\nORACLE=%#v\nSCENARIO=%s\n", seed, out, wantRes, desc.String())
			}
			if touched != wantTouched {
				t.Fatalf("seed=%d touched prod=%d oracle=%d", seed, touched, wantTouched)
			}
			t.Logf("case %4d seed=%d roles=%v sel=%v where=%v limit=%d -> rows=%d trunc=%v epoch=%d touched=%d",
				iter, seed, desc.roles, desc.sel, desc.where, desc.limit, len(out.Rows), out.Truncated, out.Epoch, touched)
		}
		if iter%150 == 0 {
			baseline := touched
			for _, noise := range []int{100, 10000} {
				_, _, touchedNoise, nd := runDifferentialCase(t, seed, noise)
				if !strings.Contains(nd.basis, "denial") && touchedNoise != baseline {
					t.Fatalf("seed=%d noise=%d touched %d != baseline %d", seed, noise, touchedNoise, baseline)
				}
			}
		}
	}
}
