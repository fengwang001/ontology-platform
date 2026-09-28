package ontology

import "testing"

// TestNewStore 占位，保证测试包可编译；具体用例随后逐个补充。
func TestNewStore(t *testing.T) {
	s := NewStore(10)
	if s == nil {
		t.Fatal("NewStore returned nil")
	}
	if got := s.LastSeq(); got != 0 {
		t.Fatalf("initial LastSeq = %d, want 0", got)
	}
	if got := s.Snapshot().Len(); got != 0 {
		t.Fatalf("initial Len = %d, want 0", got)
	}
}
