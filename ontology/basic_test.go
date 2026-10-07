package ontology

import "testing"

func TestSingleLevelFollowsSourceAndLink(t *testing.T) {
	_, st := twoLevelSchema(t)
	mkInstance(t, st, "d1", "Dept", map[string]string{"code": "D1"})
	mkInstance(t, st, "m1", "Manager", nil)

	if stt, _ := st.Resolve("m1", "deptCode"); stt.Status != StateMissingSource {
		t.Fatalf("want missing before link, got %v", stt.Status)
	}
	addLink(t, st, "memberOf", "m1", "d1")
	if rows, _ := st.QueryIndex("Manager", "deptCode", "D1"); len(rows) != 1 || rows[0].ID != "m1" {
		t.Fatalf("want m1 at D1, got %+v", rows)
	}
	setAttr(t, st, "d1", "code", "D2")
	if rows, _ := st.QueryIndex("Manager", "deptCode", "D2"); len(rows) != 1 {
		t.Fatal("want m1 moved to D2")
	}
	if rows, _ := st.QueryIndex("Manager", "deptCode", "D1"); len(rows) != 0 {
		t.Fatal("old bucket must be empty")
	}
	if _, err := st.RemoveLink("memberOf", "m1", "d1"); err != nil {
		t.Fatal(err)
	}
	if stt, _ := st.Resolve("m1", "deptCode"); stt.Status != StateMissingSource {
		t.Fatalf("want missing after remove, got %v", stt.Status)
	}
}

func TestMultiLevelPropagation(t *testing.T) {
	_, st := twoLevelSchema(t)
	mkInstance(t, st, "d1", "Dept", map[string]string{"code": "D1"})
	mkInstance(t, st, "m1", "Manager", nil)
	mkInstance(t, st, "e1", "Employee", nil)
	addLink(t, st, "memberOf", "m1", "d1")
	addLink(t, st, "reportsTo", "e1", "m1")

	if rows, _ := st.QueryIndex("Employee", "mgrDept", "D1"); len(rows) != 1 {
		t.Fatal("want e1 at D1 via two hops")
	}
	setAttr(t, st, "d1", "code", "D9")
	if rows, _ := st.QueryIndex("Employee", "mgrDept", "D9"); len(rows) != 1 {
		t.Fatal("want e1 at D9 after multi-level propagation")
	}
	if _, err := st.RemoveLink("memberOf", "m1", "d1"); err != nil {
		t.Fatal(err)
	}
	stt, _ := st.Resolve("e1", "mgrDept")
	if stt.Status != StateMissingSource {
		t.Fatalf("want missing after intermediate link removed, got %v", stt.Status)
	}
}

func TestNotUniqueImmediate(t *testing.T) {
	_, st := twoLevelSchema(t)
	mkInstance(t, st, "d1", "Dept", map[string]string{"code": "D1"})
	mkInstance(t, st, "d2", "Dept", map[string]string{"code": "D2"})
	mkInstance(t, st, "m1", "Manager", nil)
	addLink(t, st, "memberOf", "m1", "d1")
	addLink(t, st, "memberOf", "m1", "d2")

	stt, _ := st.Resolve("m1", "deptCode")
	if stt.Status != StateNotUnique {
		t.Fatalf("want not-unique, got %v", stt.Status)
	}
	for _, code := range []string{"D1", "D2"} {
		if rows, _ := st.QueryIndex("Manager", "deptCode", code); len(rows) != 0 {
			t.Fatalf("must not appear at %s while not unique", code)
		}
	}
	if _, err := st.RemoveLink("memberOf", "m1", "d1"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := st.QueryIndex("Manager", "deptCode", "D2"); len(rows) != 1 {
		t.Fatal("want recovery to D2")
	}
}
