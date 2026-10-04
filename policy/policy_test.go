package policy

import (
	"errors"
	"testing"

	"ontology/mask"
)

func i64(i int64) mask.Value   { return mask.IntVal(i) }
func sval(s string) mask.Value { return mask.StrVal([]byte(s)) }

func testCols() []Column {
	return []Column{
		{Name: "id", Type: mask.TypeInt, Def: mask.LevelPlain},
		{Name: "ssn", Type: mask.TypeStr, Def: mask.LevelNull},
		{Name: "sal", Type: mask.TypeInt, Def: mask.LevelHash},
		{Name: "dept", Type: mask.TypeStr, Def: mask.LevelPlain},
	}
}

func TestValidName(t *testing.T) {
	if !ValidName("a") || !ValidName(string(make([]byte, 64))) {
		t.Fatal("boundary names should be valid")
	}
	if ValidName("") || ValidName(string(make([]byte, 65))) {
		t.Fatal("0 and 65 byte names must be invalid")
	}
}

func TestAddTableValidationAndDuplicate(t *testing.T) {
	s := NewStore()
	if err := s.AddTable("", testCols()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty name: %v", err)
	}
	if err := s.AddTable("t", nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("0 cols: %v", err)
	}
	big := make([]Column, 33)
	for i := range big {
		big[i] = Column{Name: "c", Type: mask.TypeInt}
	}
	if err := s.AddTable("t", big); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("33 cols: %v", err)
	}
	dup := []Column{{Name: "c", Type: mask.TypeInt}, {Name: "c", Type: mask.TypeStr}}
	if err := s.AddTable("t", dup); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("dup columns: %v", err)
	}
	if err := s.AddTable("t", testCols()); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTable("t", testCols()); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("dup table: %v", err)
	}
}

func TestInsertValidation(t *testing.T) {
	s := NewStore()
	if err := s.AddTable("t", testCols()); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert("nope", make([]mask.Value, 4)); !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("missing table: %v", err)
	}
	if err := s.Insert("t", []mask.Value{i64(1)}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("wrong arity: %v", err)
	}
	row := []mask.Value{mask.NullVal(mask.TypeInt), sval("123456789"), i64(5250), sval("hr")}
	if err := s.Insert("t", row); err != nil {
		t.Fatal(err)
	}
	// NULL preserves the column type
	s.View(func(v *View) {
		tv := v.Table("t")
		r0 := tv.Rows()[0]
		if !r0[0].Null || r0[0].Type != mask.TypeInt {
			t.Fatalf("NULL cell type wrong: %+v", r0[0])
		}
	})
	// inserted data is copied
	row[1].Str[0] = 'Z'
	s.View(func(v *View) {
		if v.Table("t").Rows()[0][1].Str[0] == 'Z' {
			t.Fatal("insert aliases caller bytes")
		}
	})
}

func TestRowPolicyValidation(t *testing.T) {
	s := NewStore()
	must := func(err error, want error, ctx string) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Fatalf("%s: got %v want %v", ctx, err, want)
		}
	}
	_ = s.AddTable("t", testCols())

	must(s.AddRowPolicy(RowPolicy{ID: "", Table: "t", Role: "a", Kind: Permissive}),
		ErrInvalidArgument, "empty id")
	must(s.AddRowPolicy(RowPolicy{ID: "p", Table: "t", Role: "a", Kind: Kind(0)}),
		ErrInvalidArgument, "bad kind")
	must(s.AddRowPolicy(RowPolicy{ID: "p", Table: "t", Role: "a", Kind: Permissive,
		Pred: Predicate{Atoms: make([]Atom, 5)}}), ErrInvalidArgument, "5 atoms")
	must(s.AddRowPolicy(RowPolicy{ID: "p", Table: "missing", Role: "a", Kind: Permissive}),
		ErrTableNotFound, "missing table")
	must(s.AddRowPolicy(RowPolicy{ID: "p", Table: "t", Role: "a", Kind: Permissive,
		Pred: Predicate{Atoms: []Atom{{Col: "nope", Op: OpEq, Const: i64(1)}}}}),
		ErrColumnNotFound, "missing col")
	must(s.AddRowPolicy(RowPolicy{ID: "p", Table: "t", Role: "a", Kind: Permissive,
		Pred: Predicate{Atoms: []Atom{{Col: "id", Op: OpEq, Const: sval("x")}}}}),
		ErrInvalidArgument, "type mismatch")
	must(s.AddRowPolicy(RowPolicy{ID: "p", Table: "t", Role: "a", Kind: Permissive,
		Pred: Predicate{Atoms: []Atom{{Col: "ssn", Op: OpLt, Const: sval("x")}}}}),
		ErrInvalidArgument, "str <")

	p := RowPolicy{ID: "p", Table: "t", Role: "a", Kind: Permissive,
		Pred: Predicate{Atoms: []Atom{{Col: "id", Op: OpEq, Const: i64(1)}}}}
	must(s.AddRowPolicy(p), nil, "first add")
	must(s.AddRowPolicy(p), ErrAlreadyExists, "dup id")
	must(s.DropRowPolicy("p"), nil, "drop")
	must(s.DropRowPolicy("p"), ErrNotFound, "drop again")
	must(s.DropRowPolicy(""), ErrInvalidArgument, "drop empty id")
}

func TestMaskSetClearAndEpoch(t *testing.T) {
	s := NewStore()
	_ = s.AddTable("t", testCols())
	if s.Epoch() != 0 {
		t.Fatalf("epoch starts at 0, got %d", s.Epoch())
	}
	_ = s.Insert("t", []mask.Value{i64(1), sval("a"), i64(2), sval("hr")})
	if s.Epoch() != 0 {
		t.Fatalf("insert must not bump epoch, got %d", s.Epoch())
	}
	if err := s.SetMask("t", "id", "*", 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("star role: %v", err)
	}
	if err := s.SetMask("nope", "id", "r", 1); !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("table missing: %v", err)
	}
	if err := s.SetMask("t", "nope", "r", 1); !errors.Is(err, ErrColumnNotFound) {
		t.Fatalf("col missing: %v", err)
	}
	if err := s.SetMask("t", "id", "r", 9); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad level: %v", err)
	}
	if err := s.SetMask("t", "id", "r", 1); err != nil || s.Epoch() != 1 {
		t.Fatalf("set new rule epoch: %v %d", err, s.Epoch())
	}
	// upsert to a different level is an accepted mask change
	if err := s.SetMask("t", "id", "r", 2); err != nil || s.Epoch() != 2 {
		t.Fatalf("set existing rule epoch: %v %d", err, s.Epoch())
	}
	if err := s.AddRowPolicy(RowPolicy{ID: "p", Table: "t", Role: "r", Kind: Permissive}); err != nil ||
		s.Epoch() != 3 {
		t.Fatalf("policy epoch: %v %d", err, s.Epoch())
	}
	if err := s.ClearMask("t", "id", "r"); err != nil || s.Epoch() != 4 {
		t.Fatalf("clear epoch: %v %d", err, s.Epoch())
	}
	if err := s.ClearMask("t", "id", "r"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clear missing: %v", err)
	}
	if s.Epoch() != 4 {
		t.Fatalf("rejected clear must not bump epoch, got %d", s.Epoch())
	}
	if err := s.DropRowPolicy("p"); err != nil || s.Epoch() != 5 {
		t.Fatalf("drop epoch: %v %d", err, s.Epoch())
	}
}

func TestMaskLevelMinAndDefParticipation(t *testing.T) {
	s := NewStore()
	_ = s.AddTable("t", testCols())
	// ssn def 3; analyst rule 1; intern no rule -> 3
	if err := s.SetMask("t", "ssn", "analyst", 1); err != nil {
		t.Fatal(err)
	}
	s.View(func(v *View) {
		tv := v.Table("t")
		ssn, _ := 0, 0
		_ = ssn
		idx, _ := tv.ColumnIndex("ssn")
		lv, rules := tv.MaskLevel(idx, []string{"analyst", "intern"})
		if lv != mask.LevelPartial || rules != 1 {
			t.Fatalf("min(1,def3)=1 with 1 rule, got %d %d", lv, rules)
		}
		lv, _ = tv.MaskLevel(idx, []string{"intern", "nobody"})
		if lv != mask.LevelNull {
			t.Fatalf("no-rule roles use def 3, got %d", lv)
		}
	})
}

func TestEvalAtomNullSemantics(t *testing.T) {
	n := mask.NullVal(mask.TypeInt)
	if EvalAtom(n, Atom{Op: OpEq, Const: i64(1)}) {
		t.Fatal("NULL = const must be false")
	}
	if EvalAtom(n, Atom{Op: OpNe, Const: i64(1)}) {
		t.Fatal("NULL != const must be false")
	}
	ns := mask.NullVal(mask.TypeStr)
	if EvalAtom(ns, Atom{Op: OpNe, Const: sval("x")}) {
		t.Fatal("NULL str != const must be false")
	}
	if !EvalAtom(sval("a"), Atom{Op: OpNe, Const: sval("b")}) {
		t.Fatal("str != should be true")
	}
	if EvalAtom(sval("a"), Atom{Op: OpLt, Const: sval("b")}) {
		t.Fatal("str < is not evaluable (defensive false)")
	}
}

func TestApplicablePoliciesStarAndRoles(t *testing.T) {
	s := NewStore()
	_ = s.AddTable("t", testCols())
	add := func(id, role string, k Kind) {
		t.Helper()
		if err := s.AddRowPolicy(RowPolicy{ID: id, Table: "t", Role: role, Kind: k}); err != nil {
			t.Fatal(err)
		}
	}
	add("p1", "analyst", Permissive)
	add("p2", "auditor", Permissive)
	add("r1", "*", Restrictive)
	add("p3", "intern", Permissive)
	s.View(func(v *View) {
		tv := v.Table("t")
		got := tv.ApplicablePolicies(map[string]struct{}{"analyst": {}})
		ids := map[string]bool{}
		for _, p := range got {
			ids[p.ID] = true
		}
		if !ids["p1"] || !ids["r1"] || ids["p2"] || ids["p3"] || len(got) != 2 {
			t.Fatalf("applicable set = %v", ids)
		}
	})
}
