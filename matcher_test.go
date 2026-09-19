package ontology

import "testing"

func TestEntityPrefix(t *testing.T) {
	cases := []struct {
		entity, prefix string
		want           bool
	}{
		{"user", "user", true},
		{"user/42", "user", true},
		{"user/42/name", "user", true},
		{"superuser", "user", false}, // 前缀不得退化成子串
		{"user-x", "user", false},
		{"user2", "user", false},
		{"users", "user", false},
		{"", "", true},
		{"anything", "", true},
	}
	for _, c := range cases {
		if got := entityMatches(c.entity, c.prefix); got != c.want {
			t.Errorf("entityMatches(%q,%q)=%v want %v", c.entity, c.prefix, got, c.want)
		}
	}
}

func TestPropertySetMatching(t *testing.T) {
	d := New()
	defer d.Close()
	s, _ := d.Subscribe(SubscriptionConfig{ID: "a", Prefix: "user", Properties: []string{"name", "age"}, Buffer: 5})
	_, _ = d.Subscribe(SubscriptionConfig{ID: "b", Prefix: "user", Buffer: 5}) // 空集合=全部属性

	if ids := d.Targets("user/1", "name"); len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("name targets=%v", ids)
	}
	if ids := d.Targets("user/1", "age"); len(ids) != 2 {
		t.Fatalf("age targets=%v", ids)
	}
	if ids := d.Targets("user/1", "email"); len(ids) != 1 || ids[0] != "b" {
		t.Fatalf("email targets=%v want [b]", ids)
	}
	// 大小写与模糊匹配都不允许。
	if propertyMatches("Name", s.sub.props) {
		t.Fatal("property match must be exact")
	}
}

// Targets 必须稳定有序，且不把子串当前缀。
func TestTargetsStableOrder(t *testing.T) {
	d := New()
	defer d.Close()
	d.Subscribe(SubscriptionConfig{ID: "z", Prefix: "u", Buffer: 1})
	d.Subscribe(SubscriptionConfig{ID: "a", Prefix: "u", Buffer: 1})
	d.Subscribe(SubscriptionConfig{ID: "m", Prefix: "u", Buffer: 1})
	d.Subscribe(SubscriptionConfig{ID: "x", Prefix: "superuser", Buffer: 1})
	ids := d.Targets("u/1", "p")
	want := []string{"a", "m", "z"}
	if len(ids) != 3 {
		t.Fatalf("ids=%v want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids=%v want %v", ids, want)
		}
	}
	if ids := d.Targets("superuser/1", "p"); len(ids) != 1 || ids[0] != "x" {
		t.Fatalf("superuser targets=%v", ids)
	}
}

func TestInvalidConfig(t *testing.T) {
	d := New()
	defer d.Close()
	if _, err := d.Subscribe(SubscriptionConfig{ID: "a", Buffer: 0}); err != ErrInvalidBuffer {
		t.Fatalf("err=%v want ErrInvalidBuffer", err)
	}
	d.Subscribe(SubscriptionConfig{ID: "a", Buffer: 1})
	if _, err := d.Subscribe(SubscriptionConfig{ID: "a", Buffer: 1}); err != ErrDuplicateID {
		t.Fatalf("err=%v want ErrDuplicateID", err)
	}
	if _, err := d.Subscribe(SubscriptionConfig{Buffer: 1}); err != ErrDuplicateID {
		t.Fatalf("empty id err=%v", err)
	}
}
