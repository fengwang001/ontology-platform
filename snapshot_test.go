package ontology

import "testing"

// 调用方改快照不影响存储；后续写入也不影响旧快照。
func TestGetSnapshotIsolation(t *testing.T) {
	s := newTestStore()
	props := map[string]any{
		"name":  "walle",
		"tags":  []any{"a", "b"},
		"attrs": map[string]any{"weight": 100},
	}
	if _, err := s.Create("Robot", "r1", props); err != nil {
		t.Fatal(err)
	}

	// 入参方向的隔离：Create 之后改调用方的 map 不影响存储。
	props["name"] = "hacked"
	props["tags"].([]any)[0] = "x"
	props["attrs"].(map[string]any)["weight"] = -1

	snap, err := s.Get("Robot", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Properties["name"] != "walle" {
		t.Fatalf("store polluted by caller input: %v", snap.Properties["name"])
	}
	if snap.Properties["tags"].([]any)[0] != "a" {
		t.Fatal("nested slice not isolated from caller input")
	}
	if snap.Properties["attrs"].(map[string]any)["weight"] != 100 {
		t.Fatal("nested map not isolated from caller input")
	}

	// 出参方向的隔离：改快照不影响存储。
	snap.Properties["name"] = "mutated"
	snap.Properties["tags"].([]any)[1] = "y"
	snap.Properties["attrs"].(map[string]any)["weight"] = 0

	again, err := s.Get("Robot", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if again.Properties["name"] != "walle" {
		t.Fatalf("store polluted by snapshot mutation: %v", again.Properties["name"])
	}
	if again.Properties["tags"].([]any)[1] != "b" {
		t.Fatal("nested slice polluted by snapshot mutation")
	}
	if again.Properties["attrs"].(map[string]any)["weight"] != 100 {
		t.Fatal("nested map polluted by snapshot mutation")
	}

	// 反向隔离：后续写入不改变调用方早先拿到的快照。
	if _, err := s.Update("Robot", "r1", 1, map[string]any{"name": "eve"}); err != nil {
		t.Fatal(err)
	}
	if again.Properties["name"] != "walle" {
		t.Fatalf("earlier snapshot changed by later write: %v", again.Properties["name"])
	}
}
