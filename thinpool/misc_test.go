package thinpool

import "testing"

func TestInvalidConfigRejected(t *testing.T) {
	bad := []Config{
		{0, 200, 50, 80},
		{10, -1, 50, 80},
		{10, 200, -1, 80},
		{10, 200, 50, 101},
		{10, 200, 90, 80}, // warning > critical
	}
	for _, c := range bad {
		if _, err := New(c); err == nil {
			t.Fatalf("config %+v should be rejected", c)
		} else {
			assertErrKind(t, err, KindInvalidArgument)
		}
	}
}

func TestStringHelpers(t *testing.T) {
	if LevelNormal.String() != "normal" || LevelWarning.String() != "warning" ||
		LevelCritical.String() != "critical" {
		t.Fatal("level strings")
	}
	if KindPoolExhausted.String() != "pool_exhausted" {
		t.Fatal("kind string")
	}
	e := &Error{Kind: KindOvercommit, Msg: "x"}
	if e.Error() != "overcommit: x" {
		t.Fatalf("error text=%q", e.Error())
	}
	if classify(5, 10, 50, 80) != LevelWarning {
		t.Fatal("equality should hit warning")
	}
}
