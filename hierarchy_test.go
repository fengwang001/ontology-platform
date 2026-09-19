package projection

import "testing"

func TestWildcardDoesNotCrossLevel(t *testing.T) {
	rs, _ := Compile(Config{DefaultAllow: true, Deny: []string{"addr.*"}})
	out, err := rs.Project(sampleObject(), nil)
	if err != nil {
		t.Fatal(err)
	}
	addr := out["addr"].(map[string]any)
	if _, ok := addr["city"]; ok {
		t.Fatal("direct child city must be denied")
	}
	geo, ok := addr["geo"].(map[string]any)
	if !ok {
		t.Fatal("addr.geo must survive: addr.* does not cross levels")
	}
	if geo["lat"] != 40.7 {
		t.Fatalf("addr.geo.lat must survive, got %v", geo["lat"])
	}
}

func TestAncestorDenyCascadesInProjection(t *testing.T) {
	rs, _ := Compile(Config{
		Allow: []string{"name", "addr.geo", "addr.geo.lat"},
		Deny:  []string{"addr"},
	})
	out, err := rs.Project(sampleObject(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["addr"]; ok {
		t.Fatal("addr subtree must be fully hidden despite explicit descendant allows")
	}
}

func TestFullyPrunedNestedObjectVanishes(t *testing.T) {
	rs, _ := Compile(Config{
		DefaultAllow: true,
		Deny:         []string{"addr.city", "addr.zip", "addr.geo.lat", "addr.geo.lng"},
	})
	out, err := rs.Project(sampleObject(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["addr"]; ok {
		t.Fatalf("addr with no visible children must vanish, got %#v", out["addr"])
	}
}
