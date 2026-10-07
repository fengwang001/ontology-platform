package ontology

import "testing"

// 测试用两层 schema：
//
//	Manager.deptCode   <- memberOf -> Dept.code
//	Employee.mgrDept   <- reportsTo -> Manager.deptCode（二级传递）
//
// memberOf/reportsTo 链接本身允许一对多，但派生索引声明要求唯一取值，
// 因此多条出边时索引进入 StateNotUnique。
func twoLevelSchema(t *testing.T) (*Schema, *Store) {
	t.Helper()
	s := NewSchema()
	mustReg(s.RegisterObjectType("Dept", []PropertyName{"code"}))
	mustReg(s.RegisterObjectType("Manager", []PropertyName{"deptCode"}))
	mustReg(s.RegisterObjectType("Employee", []PropertyName{"mgrDept"}))
	mustReg(s.RegisterLinkType(LinkType{Name: "memberOf", SrcType: "Manager", DstType: "Dept"}))
	mustReg(s.RegisterLinkType(LinkType{Name: "reportsTo", SrcType: "Employee", DstType: "Manager"}))
	mustReg(s.RegisterDerivedIndex(DerivedIndex{
		SrcType: "Manager", PropName: "deptCode", Link: "memberOf", DstProp: "code"}))
	mustReg(s.RegisterDerivedIndex(DerivedIndex{
		SrcType: "Employee", PropName: "mgrDept", Link: "reportsTo", DstProp: "deptCode"}))
	return s, NewStore(s)
}

func mustReg(err error) {
	if err != nil {
		panic(err)
	}
}

func mkInstance(t *testing.T, st *Store, id, typ string, props map[string]string) {
	t.Helper()
	pmap := map[PropertyName]string{}
	for k, v := range props {
		pmap[PropertyName(k)] = v
	}
	if err := st.CreateInstance(ObjectID(id), ObjectTypeName(typ), pmap); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
}

func setAttr(t *testing.T, st *Store, id, prop, val string) {
	t.Helper()
	if _, err := st.SetAttribute(ObjectID(id), PropertyName(prop),
		PropertyValue{Val: val, Has: true}); err != nil {
		t.Fatalf("set %s.%s=%s: %v", id, prop, val, err)
	}
}

func addLink(t *testing.T, st *Store, link, from, to string) {
	t.Helper()
	if _, err := st.AddLink(LinkTypeName(link), ObjectID(from), ObjectID(to)); err != nil {
		t.Fatalf("add %s %s->%s: %v", link, from, to, err)
	}
}
