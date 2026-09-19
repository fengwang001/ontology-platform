package projection

import "testing"

func depConfig(policy SourceHiddenPolicy) Config {
	return Config{
		Deny: []string{"firstName"},
		Dependencies: []Dependency{
			{Field: "displayName", Sources: []string{"firstName", "lastName"}},
		},
		OnSourceHidden: policy,
	}
}

func depObject() map[string]any {
	return map[string]any{
		"firstName":   "Ada",
		"lastName":    "Lovelace",
		"displayName": "Ada Lovelace",
	}
}

func TestDependencyHideResultPolicy(t *testing.T) {
	rs := mustCompile(t, depConfig(HideResult))
	out, err := rs.Project(depObject())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["displayName"]; ok {
		t.Error("来源不可见时结果应按策略一并隐藏")
	}
	if _, ok := out["lastName"]; !ok {
		t.Error("可见的来源字段不应受影响")
	}
}

func TestDependencyFailPolicy(t *testing.T) {
	rs := mustCompile(t, depConfig(FailOnHiddenSource))
	_, err := rs.Project(depObject())
	de, ok := err.(*DependencyError)
	if !ok {
		t.Fatalf("应返回 *DependencyError, 得到 %T: %v", err, err)
	}
	if de.Field != "displayName" || de.Source != "firstName" {
		t.Errorf("应定位结果字段与不可见来源: %+v", de)
	}
}

func TestDependencySourceVisibleIsNoOp(t *testing.T) {
	cfg := depConfig(FailOnHiddenSource)
	cfg.Deny = nil
	rs := mustCompile(t, cfg)
	out, err := rs.Project(depObject())
	if err != nil {
		t.Fatalf("来源可见时不应报错: %v", err)
	}
	if _, ok := out["displayName"]; !ok {
		t.Error("来源可见时结果应保留")
	}
}

func TestDependencyResultAlreadyHiddenIsNoOp(t *testing.T) {
	cfg := depConfig(FailOnHiddenSource)
	cfg.Deny = []string{"firstName", "displayName"}
	rs := mustCompile(t, cfg)
	if _, err := rs.Project(depObject()); err != nil {
		t.Fatalf("结果本身已被裁掉时不应再报依赖错误: %v", err)
	}
}
