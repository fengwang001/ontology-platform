package projection

import "testing"

func TestResultMutationDoesNotAffectSource(t *testing.T) {
	rs := mustCompile(t, Config{})
	obj := sampleObject()
	out, err := rs.Project(obj)
	if err != nil {
		t.Fatal(err)
	}
	out["addr"].(map[string]any)["city"] = "Beijing"
	out["addr"].(map[string]any)["geo"].(map[string]any)["lat"] = 0.0
	out["tags"].([]any)[0] = "hacked"
	out["name"] = "Mallory"

	orig := sampleObject()
	addr := obj["addr"].(map[string]any)
	if addr["city"] != orig["addr"].(map[string]any)["city"] {
		t.Error("修改返回值的嵌套 map 影响到了原对象")
	}
	if addr["geo"].(map[string]any)["lat"] != 31.23 {
		t.Error("修改返回值的深层嵌套 map 影响到了原对象")
	}
	if obj["tags"].([]any)[0] != "eng" {
		t.Error("修改返回值的 slice 影响到了原对象")
	}
	if obj["name"] != "Ada" {
		t.Error("修改返回值的标量影响到了原对象")
	}
}

func TestSourceMutationDoesNotAffectResult(t *testing.T) {
	rs := mustCompile(t, Config{})
	obj := sampleObject()
	out, err := rs.Project(obj)
	if err != nil {
		t.Fatal(err)
	}
	obj["addr"].(map[string]any)["city"] = "Beijing"
	obj["tags"].([]any)[0] = "hacked"

	if out["addr"].(map[string]any)["city"] != "Shanghai" {
		t.Error("修改原对象影响到了已返回的投影结果")
	}
	if out["tags"].([]any)[0] != "eng" {
		t.Error("修改原对象的 slice 影响到了已返回的投影结果")
	}
}

func TestTwoResultsAreIndependent(t *testing.T) {
	rs := mustCompile(t, Config{})
	obj := sampleObject()
	out1, err := rs.Project(obj)
	if err != nil {
		t.Fatal(err)
	}
	out2, err := rs.Project(obj)
	if err != nil {
		t.Fatal(err)
	}
	out1["addr"].(map[string]any)["city"] = "Beijing"
	if out2["addr"].(map[string]any)["city"] != "Shanghai" {
		t.Error("两次投影的结果共享了嵌套结构")
	}
}
