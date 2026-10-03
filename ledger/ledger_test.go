package ledger

import "testing"

// TestLedger 表驱动覆盖账本原语。
func TestLedger(t *testing.T) {
	l := New()
	if got := l.Limit("A"); got != 0 {
		t.Errorf("未设额度 Limit=%d, want 0", got)
	}
	if got := l.Usage("A"); got != 0 {
		t.Errorf("初始 Usage=%d, want 0", got)
	}
	l.SetLimit("A", 100)
	l.Add("A", 40)
	l.Add("A", 10)
	l.Sub("A", 5)
	if got := l.Usage("A"); got != 45 {
		t.Errorf("Usage=%d, want 45", got)
	}
	// 额度可设得低于现有用量，总被接受。
	l.SetLimit("A", 5)
	if got := l.Limit("A"); got != 5 {
		t.Errorf("Limit=%d, want 5", got)
	}
	if got := l.Usage("A"); got != 45 {
		t.Errorf("降额度不应改用量 Usage=%d, want 45", got)
	}
	// 租户之间互不影响。
	l.Add("B", 7)
	if got := l.Usage("B"); got != 7 {
		t.Errorf("Usage(B)=%d, want 7", got)
	}
	if got := l.Usage("A"); got != 45 {
		t.Errorf("Usage(A)=%d, want 45", got)
	}
}
