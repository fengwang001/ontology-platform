package ontology

import (
	"slices"
	"testing"
)

func mustSub(t *testing.T, d *Dispatcher, prefix string, props []string) *Subscription {
	t.Helper()
	s, err := d.Subscribe(prefix, props, SubscribeOptions{Buffer: 8, Overflow: DropNewest})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// 前缀匹配不得退化成子串匹配：user 不得匹配 superuser。
func TestPrefixIsNotSubstring(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSub(t, d, "user", nil)

	if ids := d.Match("superuser", "name"); len(ids) != 0 {
		t.Fatalf("Match(superuser) = %v, want empty", ids)
	}
	for _, entity := range []string{"user", "user1", "user/42", "user.profile"} {
		if ids := d.Match(entity, "name"); !slices.Equal(ids, []uint64{s.ID()}) {
			t.Fatalf("Match(%q) = %v, want [%d]", entity, ids, s.ID())
		}
	}
	for _, entity := range []string{"superuser", "us", "auser", "USER"} {
		if ids := d.Match(entity, "name"); len(ids) != 0 {
			t.Fatalf("Match(%q) = %v, want empty", entity, ids)
		}
	}

	// 实际投递验证：superuser 不投递，user1 投递。
	if _, err := d.Publish("superuser", "name", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Publish("user1", "name", 2); err != nil {
		t.Fatal(err)
	}
	m := <-s.C()
	if m.Entity != "user1" || m.Seq != 2 {
		t.Fatalf("got %+v, want entity user1 seq 2", m)
	}
}

// 属性名必须全等；空集合匹配该实体的所有属性。
func TestPropertyMatchingIsExact(t *testing.T) {
	d := New()
	defer d.Close()
	named := mustSub(t, d, "user", []string{"name", "email"})
	all := mustSub(t, d, "user", nil)

	if ids := d.Match("user1", "name"); !slices.Equal(ids, []uint64{named.ID(), all.ID()}) {
		t.Fatalf("Match(name) = %v", ids)
	}
	for _, prop := range []string{"Name", "name2", "nam", "names", " name"} {
		if ids := d.Match("user1", prop); !slices.Equal(ids, []uint64{all.ID()}) {
			t.Fatalf("Match(%q) = %v, want only the catch-all subscriber", prop, ids)
		}
	}
}

// Match 的结果必须稳定有序（按订阅 ID 升序）。
func TestMatchIsSortedAndStable(t *testing.T) {
	d := New()
	defer d.Close()
	var want []uint64
	for i := 0; i < 8; i++ {
		s := mustSub(t, d, "user", nil)
		want = append(want, s.ID())
	}
	for i := 0; i < 20; i++ {
		if ids := d.Match("user1", "anything"); !slices.Equal(ids, want) {
			t.Fatalf("Match = %v, want %v", ids, want)
		}
	}
}

// 已取消或已断开的订阅者不应出现在 Match 结果中。
func TestMatchExcludesCanceled(t *testing.T) {
	d := New()
	defer d.Close()
	a := mustSub(t, d, "user", nil)
	b := mustSub(t, d, "user", nil)
	a.Cancel()
	if ids := d.Match("user1", "x"); !slices.Equal(ids, []uint64{b.ID()}) {
		t.Fatalf("Match = %v, want [%d]", ids, b.ID())
	}
}
