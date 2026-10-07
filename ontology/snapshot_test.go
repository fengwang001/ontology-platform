package ontology

import "testing"

func TestSnapshotReflectsCommittedTruth(t *testing.T) {
	_, st := twoLevelSchema(t)
	mkInstance(t, st, "d1", "Dept", map[string]string{"code": "D1"})
	mkInstance(t, st, "m1", "Manager", nil)
	addLink(t, st, "memberOf", "m1", "d1")

	snap := st.Snapshot()
	d := snap.data
	if d.inst["d1"].props["code"].Val != "D1" {
		t.Fatal("snapshot must contain source value")
	}
	if _, ok := d.inst["m1"].out["memberOf"]["d1"]; !ok {
		t.Fatal("snapshot must contain committed link")
	}
	if got := d.incoming["d1"]["memberOf"]; len(got) != 1 || got[0] != "m1" {
		t.Fatalf("snapshot incoming index wrong: %v", got)
	}
	bucket := d.index[propKey{typ: "Manager", prop: "deptCode"}]["D1"]
	if len(bucket) != 1 || bucket[0] != "m1" {
		t.Fatalf("snapshot index bucket wrong: %v", bucket)
	}

	// 快照是深拷贝：提交新值后旧快照不变。
	setAttr(t, st, "d1", "code", "D2")
	if d.inst["d1"].props["code"].Val != "D1" {
		t.Fatal("snapshot must remain immutable after later commit")
	}
	if st.Snapshot().data.inst["d1"].props["code"].Val != "D2" {
		t.Fatal("fresh snapshot must see new value")
	}
}

func TestIdempotentOpsAndUnsetSource(t *testing.T) {
	_, st := twoLevelSchema(t)
	mkInstance(t, st, "d1", "Dept", map[string]string{"code": "D1"})
	mkInstance(t, st, "m1", "Manager", nil)
	addLink(t, st, "memberOf", "m1", "d1")

	// 幂等：重复加边/删不存在的边不产生传播层，也不报错。
	if levels, err := st.AddLink("memberOf", "m1", "d1"); err != nil || levels != nil {
		t.Fatalf("duplicate add must be no-op, got levels=%v err=%v", levels, err)
	}
	if levels, err := st.RemoveLink("memberOf", "m1", "d1"); err != nil {
		t.Fatal(err)
	} else {
		addLink(t, st, "memberOf", "m1", "d1")
		_ = levels
	}
	if _, err := st.RemoveLink("memberOf", "m1", "d1"); err != nil {
		t.Fatal(err)
	}
	if levels, err := st.RemoveLink("memberOf", "m1", "d1"); err != nil || levels != nil {
		t.Fatalf("remove missing edge must be no-op, got %v %v", levels, err)
	}

	// 源属性清空：唯一目标但无值 -> StateNoValue。
	addLink(t, st, "memberOf", "m1", "d1")
	if _, err := st.SetAttribute("d1", "code", PropertyValue{}); err != nil {
		t.Fatal(err)
	}
	stt, _ := st.Resolve("m1", "deptCode")
	if stt.Status != StateNoValue {
		t.Fatalf("want no-value, got %v", stt.Status)
	}
	if rows, _ := st.QueryIndex("Manager", "deptCode", "D1"); len(rows) != 0 {
		t.Fatal("unset source must not appear in index")
	}

	// 不能直接写派生属性。
	_, err := st.SetAttribute("m1", "deptCode", PropertyValue{Val: "x", Has: true})
	if ErrorKindOf(err) != KindLinkTypeNotSupported {
		t.Fatalf("want link-not-supported when writing derived prop, got %v", err)
	}
}
