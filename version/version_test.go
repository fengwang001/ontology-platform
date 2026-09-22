package version

import "testing"

func TestZeroSemantics(t *testing.T) {
	if !None.IsZero() {
		t.Fatal("None 必须是零值")
	}
	if New(5).IsZero() {
		t.Fatal("非零版本不能被判定为零值")
	}
}

func TestCompare(t *testing.T) {
	if !New(1).Before(New(2)) {
		t.Fatal("1 应早于 2")
	}
	if None.Before(None) {
		t.Fatal("None 不早于自身")
	}
	if !None.Before(New(1)) {
		t.Fatal("None 应早于任意非零版本")
	}
	if !New(3).After(New(2)) || !New(3).Newer(New(2)) {
		t.Fatal("3 应新于 2")
	}
	if got := New(2).Max(New(5)); got != New(5) {
		t.Fatalf("Max = %v, want 5", got)
	}
	if got := New(9).Max(New(5)); got != New(9) {
		t.Fatalf("Max = %v, want 9", got)
	}
}
