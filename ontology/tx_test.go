package ontology

import (
	"errors"
	"fmt"
	"testing"
)

func TestAtomicRollbackOnDownstreamFailure(t *testing.T) {
	_, st := twoLevelSchema(t)
	mkInstance(t, st, "d1", "Dept", map[string]string{"code": "D1"})
	mkInstance(t, st, "m1", "Manager", nil)
	addLink(t, st, "memberOf", "m1", "d1")

	var calls int
	st.SetIndexFailHook(func() error {
		calls++
		return errors.New("injected index failure")
	})
	_, err := st.SetAttribute("d1", "code", PropertyValue{Val: "DX", Has: true})
	if ErrorKindOf(err) != KindDownstreamUpdateFailed {
		t.Fatalf("want downstream failure, got %v", err)
	}
	st.SetIndexFailHook(nil)

	v, has, _ := st.NativeAttribute("d1", "code")
	if !has || v.Val != "D1" {
		t.Fatalf("source write must roll back, got %q has=%v", v.Val, has)
	}
	if rows, _ := st.QueryIndex("Manager", "deptCode", "D1"); len(rows) != 1 {
		t.Fatal("downstream index must remain at D1")
	}
	if rows, _ := st.QueryIndex("Manager", "deptCode", "DX"); len(rows) != 0 {
		t.Fatal("no partial update to DX allowed")
	}
	if calls == 0 {
		t.Fatal("fail hook was never invoked")
	}
}

func TestDeleteCascade(t *testing.T) {
	_, st := twoLevelSchema(t)
	mkInstance(t, st, "d1", "Dept", map[string]string{"code": "D1"})
	mkInstance(t, st, "d2", "Dept", map[string]string{"code": "D2"})
	mkInstance(t, st, "m1", "Manager", nil)
	mkInstance(t, st, "m2", "Manager", nil)
	mkInstance(t, st, "m3", "Manager", nil)
	mkInstance(t, st, "e1", "Employee", nil)
	addLink(t, st, "memberOf", "m1", "d1")
	addLink(t, st, "memberOf", "m2", "d1")
	addLink(t, st, "memberOf", "m3", "d2")
	addLink(t, st, "reportsTo", "e1", "m1")

	if _, err := st.DeleteInstance("e1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Resolve("e1", "mgrDept"); ErrorKindOf(err) != KindSourceNotFound {
		t.Fatalf("deleted downstream must vanish, got %v", err)
	}
	if rows, _ := st.QueryIndex("Manager", "deptCode", "D1"); len(rows) != 2 {
		t.Fatalf("m1,m2 must remain, got %v", rows)
	}

	if _, err := st.DeleteInstance("d1"); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"m1", "m2"} {
		if stt, _ := st.Resolve(ObjectID(m), "deptCode"); stt.Status != StateMissingSource {
			t.Fatalf("%s must be unindexable after source delete, got %v", m, stt.Status)
		}
	}
	if rows, _ := st.QueryIndex("Manager", "deptCode", "D1"); len(rows) != 0 {
		t.Fatalf("no rows may reference deleted source, got %v", rows)
	}
	if rows, _ := st.QueryIndex("Manager", "deptCode", "D2"); len(rows) != 1 || rows[0].ID != "m3" {
		t.Fatalf("m3 must remain indexed at D2, got %v", rows)
	}
}

func TestExactDownstreamCountNoDuplicates(t *testing.T) {
	_, st := twoLevelSchema(t)
	mkInstance(t, st, "d1", "Dept", map[string]string{"code": "D1"})
	const n = 50
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("m%02d", i)
		mkInstance(t, st, id, "Manager", nil)
		addLink(t, st, "memberOf", id, "d1")
	}
	mkInstance(t, st, "d2", "Dept", map[string]string{"code": "D2"})
	mkInstance(t, st, "mOther", "Manager", nil)
	addLink(t, st, "memberOf", "mOther", "d2")

	levels, err := st.SetAttribute("d1", "code", PropertyValue{Val: "DN", Has: true})
	if err != nil {
		t.Fatal(err)
	}
	total, seen := 0, map[ObjectID]bool{}
	for _, lv := range levels {
		total += len(lv.instances)
		for _, id := range lv.instances {
			if seen[id] {
				t.Fatalf("downstream %s processed twice", id)
			}
			seen[id] = true
		}
	}
	if total != n {
		t.Fatalf("touched %d, want exactly %d real downstreams", total, n)
	}
	if seen["mOther"] {
		t.Fatal("downstream pointing elsewhere must not be touched")
	}
	if rows, _ := st.QueryIndex("Manager", "deptCode", "DN"); len(rows) != n {
		t.Fatalf("want %d rows at DN, got %d", n, len(rows))
	}
}

func TestTripleInterleavingSerialEquivalence(t *testing.T) {
	perms := [][]int{
		{0, 1, 2}, {0, 2, 1}, {1, 0, 2},
		{1, 2, 0}, {2, 0, 1}, {2, 1, 0},
	}
	for p, order := range perms {
		_, st := twoLevelSchema(t)
		mkInstance(t, st, "d1", "Dept", map[string]string{"code": "D1"})
		mkInstance(t, st, "d2", "Dept", map[string]string{"code": "D2"})
		mkInstance(t, st, "m1", "Manager", nil)
		addLink(t, st, "memberOf", "m1", "d1")

		base := []Op{
			{Kind: OpSetAttribute, ID: "d2", Prop: "code",
				Val: PropertyValue{Val: "D2N", Has: true}},
			{Kind: OpRemoveLink, ID: "m1", Link: "memberOf", To: "d1"},
			{Kind: OpAddLink, ID: "m1", Link: "memberOf", To: "d2"},
		}
		ops := make([]Op, 3)
		for i, idx := range order {
			ops[i] = base[idx]
		}
		if _, err := st.Multi(ops); err != nil {
			t.Fatalf("perm %d: %v", p, err)
		}
		stt, _ := st.Resolve("m1", "deptCode")
		if stt.Status != StateIndexable || stt.Value.Val != "D2N" {
			t.Fatalf("perm %d: want D2N indexable, got %+v", p, stt)
		}
		if rows, _ := st.QueryIndex("Manager", "deptCode", "D2N"); len(rows) != 1 {
			t.Fatalf("perm %d: want exactly m1 at D2N", p)
		}
	}
}
