package access_test

import (
	"bytes"
	"errors"
	"testing"

	"ontology/access"
	"ontology/mask"
	"ontology/policy"
)

func iv(i int64) mask.Value  { return mask.IntVal(i) }
func sv(s string) mask.Value { return mask.StrVal([]byte(s)) }

func strs(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = prefix + "-" + string(rune('a'+i%26)) + "-" + itoa(i/26)
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func eqCell(a, b mask.Value) bool {
	if a.Null != b.Null || a.Type != b.Type {
		return false
	}
	if a.Null {
		return true
	}
	if a.Type == mask.TypeInt {
		return a.Int == b.Int
	}
	return bytes.Equal(a.Str, b.Str)
}

func exampleStore(t *testing.T) *policy.Store {
	t.Helper()
	s := policy.NewStore()
	cols := []policy.Column{
		{Name: "id", Type: mask.TypeInt, Def: mask.LevelPlain},
		{Name: "ssn", Type: mask.TypeStr, Def: mask.LevelNull},
		{Name: "sal", Type: mask.TypeInt, Def: mask.LevelHash},
		{Name: "dept", Type: mask.TypeStr, Def: mask.LevelPlain},
	}
	if err := s.AddTable("t", cols); err != nil {
		t.Fatal(err)
	}
	rows := [][]mask.Value{
		{iv(1), sv("123456789"), iv(5250), sv("hr")},
		{iv(2), sv("987"), iv(-250), sv("it")},
		{iv(3), mask.NullVal(mask.TypeStr), iv(7000), sv("hr")},
	}
	for _, r := range rows {
		if err := s.Insert("t", r); err != nil {
			t.Fatal(err)
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.AddRowPolicy(policy.RowPolicy{ID: "P1", Table: "t", Role: "analyst", Kind: policy.Permissive,
		Pred: policy.Predicate{Atoms: []policy.Atom{{Col: "dept", Op: policy.OpEq, Const: sv("hr")}}}}))
	must(s.AddRowPolicy(policy.RowPolicy{ID: "P2", Table: "t", Role: "auditor", Kind: policy.Permissive}))
	must(s.AddRowPolicy(policy.RowPolicy{ID: "R1", Table: "t", Role: "*", Kind: policy.Restrictive,
		Pred: policy.Predicate{Atoms: []policy.Atom{{Col: "sal", Op: policy.OpLe, Const: iv(6000)}}}}))
	must(s.SetMask("t", "ssn", "analyst", mask.LevelPartial))
	must(s.SetMask("t", "sal", "analyst", mask.LevelPartial))
	must(s.SetMask("t", "sal", "auditor", mask.LevelNull))
	return s
}

func TestSpecExample(t *testing.T) {
	r := access.NewReader(exampleStore(t))

	res, err := r.Read([]string{"analyst"}, "t", []string{"ssn", "sal"},
		policy.Predicate{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0].RowNo != 1 || res.Truncated {
		t.Fatalf("analyst rows = %+v truncated=%v", res.Rows, res.Truncated)
	}
	if got := res.Rows[0].Cells[0]; got.Null || string(got.Str) != "*****6789" {
		t.Fatalf("ssn = %q", got.Str)
	}
	if got := res.Rows[0].Cells[1]; got.Null || got.Int != 5200 {
		t.Fatalf("sal = %+v", got)
	}

	res, err = r.Read([]string{"analyst"}, "t", []string{"sal"},
		policy.Predicate{Atoms: []policy.Atom{{Col: "sal", Op: policy.OpEq, Const: iv(5250)}}}, 100)
	if err != nil || len(res.Rows) != 0 {
		t.Fatalf("probe plaintext: err=%v rows=%d", err, len(res.Rows))
	}
	res, err = r.Read([]string{"analyst"}, "t", []string{"sal"},
		predicateSalEq(5200), 100)
	if err != nil || len(res.Rows) != 1 || res.Rows[0].RowNo != 1 {
		t.Fatalf("masked filter: err=%v rows=%v", err, res.Rows)
	}

	res, err = r.Read([]string{"analyst", "auditor"}, "t", []string{"ssn", "sal"},
		policy.Predicate{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 || res.Rows[0].RowNo != 1 || res.Rows[1].RowNo != 2 {
		t.Fatalf("dual-role rows = %v", res.Rows)
	}
	if string(res.Rows[1].Cells[0].Str) != "***" || res.Rows[1].Cells[1].Int != -300 {
		t.Fatalf("row2 = %q %d", res.Rows[1].Cells[0].Str, res.Rows[1].Cells[1].Int)
	}

	res, err = r.Read([]string{"auditor"}, "t", []string{"ssn", "sal"},
		policy.Predicate{Atoms: []policy.Atom{{Col: "sal", Op: policy.OpEq, Const: iv(6000)}}}, 100)
	if err != nil {
		t.Fatalf("level-3 column atom is false without type error: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("auditor level3 where must match nothing, rows=%d", len(res.Rows))
	}
	// on a level-3 (NULL) column, < / <= still type-checks against the int type,
	// but the atom evaluates false rather than raising a type error.
	res, err = r.Read([]string{"auditor"}, "t", []string{"ssn", "sal"},
		policy.Predicate{Atoms: []policy.Atom{{Col: "sal", Op: policy.OpLe, Const: iv(6000)}}}, 100)
	if err != nil || len(res.Rows) != 0 {
		t.Fatalf("auditor level3 <=: err=%v rows=%d", err, len(res.Rows))
	}

	res, err = r.Read([]string{"intern"}, "t", []string{"id"}, policy.Predicate{}, 100)
	if err != nil {
		t.Fatalf("default deny must not error: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("intern sees %d rows", len(res.Rows))
	}
}

func predicateSalEq(v int64) policy.Predicate {
	return policy.Predicate{Atoms: []policy.Atom{{Col: "sal", Op: policy.OpEq, Const: iv(v)}}}
}

func TestReadParameterAndExistenceErrors(t *testing.T) {
	r := access.NewReader(exampleStore(t))
	cases := []struct {
		name  string
		roles []string
		table string
		sel   []string
		where policy.Predicate
		limit int
		want  error
	}{
		{"no roles", nil, "t", []string{"id"}, policy.Predicate{}, 10, policy.ErrInvalidArgument},
		{"9 roles", strs("r", 9), "t", []string{"id"}, policy.Predicate{}, 10, policy.ErrInvalidArgument},
		{"dup roles", []string{"a", "a"}, "t", []string{"id"}, policy.Predicate{}, 10, policy.ErrInvalidArgument},
		{"star role", []string{"*"}, "t", []string{"id"}, policy.Predicate{}, 10, policy.ErrInvalidArgument},
		{"no select", []string{"a"}, "t", nil, policy.Predicate{}, 10, policy.ErrInvalidArgument},
		{"33 select", []string{"a"}, "t", append([]string{"id"}, strs("c", 32)...),
			policy.Predicate{}, 10, policy.ErrInvalidArgument},
		{"dup select", []string{"a"}, "t", []string{"id", "id"}, policy.Predicate{}, 10, policy.ErrInvalidArgument},
		{"limit 0", []string{"a"}, "t", []string{"id"}, policy.Predicate{}, 0, policy.ErrInvalidArgument},
		{"limit 1001", []string{"a"}, "t", []string{"id"}, policy.Predicate{}, 1001, policy.ErrInvalidArgument},
		{"5 where atoms", []string{"a"}, "t", []string{"id"},
			policy.Predicate{Atoms: make([]policy.Atom, 5)}, 10, policy.ErrInvalidArgument},
		{"bad table", []string{"a"}, "nope", []string{"id"}, policy.Predicate{}, 10, policy.ErrTableNotFound},
		{"bad select col", []string{"a"}, "t", []string{"nope"}, policy.Predicate{}, 10, policy.ErrColumnNotFound},
		{"bad where col", []string{"a"}, "t", []string{"id"},
			policy.Predicate{Atoms: []policy.Atom{{Col: "nope", Op: policy.OpEq, Const: iv(1)}}}, 10,
			policy.ErrColumnNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Read(tc.roles, tc.table, tc.sel, tc.where, tc.limit)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func denyStore(t *testing.T) *policy.Store {
	t.Helper()
	s := policy.NewStore()
	cols := []policy.Column{
		{Name: "a", Type: mask.TypeInt, Def: mask.LevelPlain},
		{Name: "b", Type: mask.TypeInt, Def: mask.LevelPlain},
		{Name: "c", Type: mask.TypeInt, Def: mask.LevelPlain},
		{Name: "h", Type: mask.TypeInt, Def: mask.LevelHash},
		{Name: "s", Type: mask.TypeStr, Def: mask.LevelPlain},
	}
	if err := s.AddTable("t", cols); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert("t", []mask.Value{iv(1), iv(2), iv(3), iv(42), sv("x")}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"b", "c"} {
		if err := s.SetMask("t", c, "u", mask.LevelDeny); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestDenyAndTypeMismatchOrdering(t *testing.T) {
	r := access.NewReader(denyStore(t))

	_, err := r.Read([]string{"u"}, "t", []string{"b"},
		policy.Predicate{Atoms: []policy.Atom{{Col: "c", Op: policy.OpEq, Const: iv(3)}}}, 10)
	if !errors.Is(err, access.ErrColumnDenied) {
		t.Fatalf("select deny must be reported first: %v", err)
	}

	_, err = r.Read([]string{"u"}, "t", []string{"a"},
		policy.Predicate{Atoms: []policy.Atom{
			{Col: "c", Op: policy.OpEq, Const: iv(3)},
			{Col: "b", Op: policy.OpEq, Const: iv(2)},
		}}, 10)
	if !errors.Is(err, access.ErrColumnDenied) {
		t.Fatalf("where deny in atom order: %v", err)
	}

	_, err = r.Read([]string{"u"}, "t", []string{"a"},
		policy.Predicate{Atoms: []policy.Atom{{Col: "h", Op: policy.OpEq, Const: iv(42)}}}, 10)
	if !errors.Is(err, access.ErrTypeMismatch) {
		t.Fatalf("hashed int vs int const: %v", err)
	}
	_, err = r.Read([]string{"u"}, "t", []string{"a"},
		policy.Predicate{Atoms: []policy.Atom{{Col: "h", Op: policy.OpLt, Const: sv("zz")}}}, 10)
	if !errors.Is(err, access.ErrTypeMismatch) {
		t.Fatalf("< on masked str: %v", err)
	}

	s := policy.NewStore()
	_ = s.AddTable("t", []policy.Column{
		{Name: "a", Type: mask.TypeInt, Def: mask.LevelPlain},
		{Name: "s", Type: mask.TypeStr, Def: mask.LevelNull},
	})
	_ = s.Insert("t", []mask.Value{iv(1), sv("x")})
	_ = s.AddRowPolicy(policy.RowPolicy{ID: "p", Table: "t", Role: "*", Kind: policy.Permissive})
	r2 := access.NewReader(s)
	res, err := r2.Read([]string{"u"}, "t", []string{"a"},
		policy.Predicate{Atoms: []policy.Atom{{Col: "s", Op: policy.OpLe, Const: sv("z")}}}, 10)
	if err != nil {
		t.Fatalf("level-3 atom is false, not a type error: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("level-3 where filters every row, got %d", len(res.Rows))
	}
}

func TestHashTypeChangeAllowsStrFilter(t *testing.T) {
	s := policy.NewStore()
	if err := s.AddTable("t", []policy.Column{
		{Name: "h", Type: mask.TypeInt, Def: mask.LevelHash}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert("t", []mask.Value{iv(42)}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddRowPolicy(policy.RowPolicy{ID: "p", Table: "t",
		Role: "*", Kind: policy.Permissive}); err != nil {
		t.Fatal(err)
	}
	r := access.NewReader(s)
	hashed := mask.Apply(iv(42), mask.LevelHash)
	res, err := r.Read([]string{"u"}, "t", []string{"h"},
		policy.Predicate{Atoms: []policy.Atom{
			{Col: "h", Op: policy.OpEq, Const: mask.StrVal(hashed.Str)}}}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 ||
		res.Rows[0].Cells[0].Type != mask.TypeStr ||
		!bytes.Equal(res.Rows[0].Cells[0].Str, hashed.Str) {
		t.Fatalf("hashed read = %+v", res.Rows)
	}
}

func TestLimitAndTruncated(t *testing.T) {
	s := policy.NewStore()
	if err := s.AddTable("t", []policy.Column{
		{Name: "id", Type: mask.TypeInt, Def: mask.LevelPlain}}); err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 5; i++ {
		if err := s.Insert("t", []mask.Value{iv(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddRowPolicy(policy.RowPolicy{ID: "p", Table: "t",
		Role: "*", Kind: policy.Permissive}); err != nil {
		t.Fatal(err)
	}
	r := access.NewReader(s)

	res, err := r.Read([]string{"u"}, "t", []string{"id"}, policy.Predicate{}, 5)
	if err != nil || len(res.Rows) != 5 || res.Truncated {
		t.Fatalf("limit == result count: %d truncated=%v err=%v", len(res.Rows), res.Truncated, err)
	}
	res, err = r.Read([]string{"u"}, "t", []string{"id"}, policy.Predicate{}, 4)
	if err != nil || len(res.Rows) != 4 || !res.Truncated {
		t.Fatalf("limit 4 of 5: %d truncated=%v", len(res.Rows), res.Truncated)
	}
	if res.Rows[0].RowNo != 1 || res.Rows[3].RowNo != 4 {
		t.Fatalf("row numbering/order wrong: %v", res.Rows)
	}
}

func TestEpochReportedAndAdvanced(t *testing.T) {
	s := policy.NewStore()
	_ = s.AddTable("t", []policy.Column{{Name: "id", Type: mask.TypeInt, Def: mask.LevelPlain}})
	_ = s.Insert("t", []mask.Value{iv(1)})
	_ = s.AddRowPolicy(policy.RowPolicy{ID: "p", Table: "t", Role: "*", Kind: policy.Permissive})
	r := access.NewReader(s)
	res, err := r.Read([]string{"u"}, "t", []string{"id"}, policy.Predicate{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Epoch != 1 {
		t.Fatalf("epoch = %d want 1 (one policy)", res.Epoch)
	}
	if err := s.SetMask("t", "id", "u", mask.LevelPartial); err != nil {
		t.Fatal(err)
	}
	res, _ = r.Read([]string{"u"}, "t", []string{"id"}, policy.Predicate{}, 10)
	if res.Epoch != 2 {
		t.Fatalf("epoch = %d want 2", res.Epoch)
	}
	// subject "u" has an explicit level-1 rule on id -> floor(1/100)*100 = 0
	if res.Rows[0].Cells[0].Int != 0 {
		t.Fatalf("id=1 at level 1 -> 0, got %d", res.Rows[0].Cells[0].Int)
	}
	// a different subject with no rule participates with def 0 -> plaintext
	res2, err := r.Read([]string{"other"}, "t", []string{"id"}, policy.Predicate{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Rows[0].Cells[0].Int != 1 {
		t.Fatalf("no-rule role uses def 0 -> plaintext 1, got %d", res2.Rows[0].Cells[0].Int)
	}
}

func TestRejectedReadChangesNothing(t *testing.T) {
	s := denyStore(t)
	before := s.Epoch()
	r := access.NewReader(s)
	_, err := r.Read([]string{"u"}, "t", []string{"b"}, policy.Predicate{}, 10)
	if !errors.Is(err, access.ErrColumnDenied) {
		t.Fatalf("want deny, got %v", err)
	}
	if s.Epoch() != before {
		t.Fatal("read must never change the epoch")
	}
	// denied column value never leaks through touched side effects: still denied
	_, err = r.Read([]string{"u"}, "t", []string{"b"}, policy.Predicate{}, 10)
	if !errors.Is(err, access.ErrColumnDenied) {
		t.Fatalf("state changed after rejected read: %v", err)
	}
}
