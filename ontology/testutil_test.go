package ontologyindex

import "testing"

// schemaForTests 构造一个含两版本迁移的类型：
//
//	v1 自 ts=1 生效：物理字段 ssn(prop_ssn, 唯一) 与 name(prop_name)
//	v2 自 ts=100 生效：ssn 废弃并由 tax_id(prop_tax) 替代；
//	                    name 直接废弃且无替代（E1 场景）。
func schemaForTests() *Schema {
	s := NewSchema()
	s.AddType(&ObjectType{
		Name: "Person",
		Versions: []TypeVersion{
			{EffectiveAt: 1, Fields: []FieldVersion{
				{Physical: "ssn", PropertyID: "prop_ssn"},
				{Physical: "name", PropertyID: "prop_name"},
			}},
			{EffectiveAt: 100, Fields: []FieldVersion{
				{Physical: "ssn", PropertyID: "prop_ssn", Deprecated: true, ReplacedBy: "prop_tax"},
				{Physical: "tax_id", PropertyID: "prop_tax"},
				{Physical: "name", PropertyID: "prop_name", Deprecated: true},
			}},
			{EffectiveAt: 200, Fields: []FieldVersion{
				{Physical: "ssn", PropertyID: "prop_ssn", Deprecated: true, ReplacedBy: "prop_tax"},
				{Physical: "tax_id", PropertyID: "prop_tax"},
				{Physical: "name", PropertyID: "prop_name", Deprecated: true},
				{Physical: "nickname", PropertyID: "prop_nick"},
			}},
		},
	})
	return s
}

func lookupSet(t *testing.T, eng *Engine, idx string, v Value) map[string]bool {
	t.Helper()
	got, err := eng.Lookup(idx, v)
	if err != nil {
		t.Fatalf("lookup 意外失败: %v", err)
	}
	m := map[string]bool{}
	for _, id := range got {
		m[id] = true
	}
	return m
}

func expectErrorCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	ie, ok := err.(*IndexError)
	if !ok {
		t.Fatalf("期望 *IndexError, 得到 %T: %v", err, err)
	}
	if ie.Code != want {
		t.Fatalf("期望错误码 %d, 得到 %d (%v)", want, ie.Code, err)
	}
}
