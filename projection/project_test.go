package projection

import "testing"

func sampleObject() map[string]any {
	return map[string]any{
		"name":   "Ada",
		"age":    36,
		"salary": 123456,
		"addr": map[string]any{
			"city":   "Shanghai",
			"street": "Nanjing Rd",
			"geo":    map[string]any{"lat": 31.23, "lng": 121.47},
		},
		"tags": []any{"eng", "lead"},
	}
}

func TestProjectDeniesTopLevelField(t *testing.T) {
	rs := mustCompile(t, Config{Deny: []string{"salary"}})
	out, err := rs.Project(sampleObject())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["salary"]; ok {
		t.Error("salary 应被裁掉")
	}
	if len(out) != 4 {
		t.Errorf("应保留 4 个字段, 得到 %d", len(out))
	}
}

func TestProjectRequiredFieldCutReportsRule(t *testing.T) {
	rs := mustCompile(t, Config{
		Deny:     []string{"name"},
		Required: []string{"name"},
	})
	_, err := rs.Project(sampleObject())
	rfe, ok := err.(*RequiredFieldError)
	if !ok {
		t.Fatalf("应返回 *RequiredFieldError, 得到 %T: %v", err, err)
	}
	if rfe.Field != "name" || rfe.Rule != "name" {
		t.Errorf("应定位必填字段与裁剪规则: %+v", rfe)
	}
}

func TestProjectRequiredFieldMissingWithoutRule(t *testing.T) {
	rs := mustCompile(t, Config{Required: []string{"nickname"}})
	_, err := rs.Project(sampleObject())
	rfe, ok := err.(*RequiredFieldError)
	if !ok || rfe.Field != "nickname" || rfe.Rule != "" {
		t.Fatalf("源对象缺少必填字段也应报错且无规则: %v", err)
	}
}

func TestProjectWildcardDoesNotCrossLevels(t *testing.T) {
	rs := mustCompile(t, Config{
		Deny:  []string{"addr.*"},
		Allow: []string{"addr.geo"},
	})
	out, err := rs.Project(sampleObject())
	if err != nil {
		t.Fatal(err)
	}
	addr, ok := out["addr"].(map[string]any)
	if !ok {
		t.Fatal("addr 应保留")
	}
	if _, ok := addr["city"]; ok {
		t.Error("addr.city 应被 addr.* 裁掉")
	}
	geo, ok := addr["geo"].(map[string]any)
	if !ok {
		t.Fatal("addr.geo 应被更具体的允许规则保留")
	}
	if _, ok := geo["lat"]; !ok {
		t.Error("addr.geo.lat 不应被 addr.* 波及")
	}
}

func TestProjectParentDenyDropsWholeSubtree(t *testing.T) {
	rs := mustCompile(t, Config{
		Deny:  []string{"addr"},
		Allow: []string{"addr.city"},
	})
	out, err := rs.Project(sampleObject())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["addr"]; ok {
		t.Error("父字段被拒后整个子树应消失，即使后代被显式允许")
	}
}

func TestProjectEmptyNestedObjectDisappears(t *testing.T) {
	rs := mustCompile(t, Config{Deny: []string{"addr.*"}})
	out, err := rs.Project(sampleObject())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["addr"]; ok {
		t.Error("addr 所有子字段被裁后 addr 本身应消失")
	}
}

func TestProjectKeepsOriginallyEmptyObject(t *testing.T) {
	rs := mustCompile(t, Config{})
	obj := sampleObject()
	obj["meta"] = map[string]any{}
	out, err := rs.Project(obj)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["meta"]; !ok {
		t.Error("原本就为空的嵌套对象不应被投影移除")
	}
}
