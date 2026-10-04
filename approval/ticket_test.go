package approval

import "testing"

func TestTicketValidityBoundary(t *testing.T) {
	tk := New(100)
	if err := tk.Add("bob", 10); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	cases := []struct {
		now   int64
		valid int
	}{
		{109, 1}, // 早 1 秒仍有效
		{110, 0}, // 恰等 at+TTL 即失效
	}
	for _, tc := range cases {
		if got := tk.ValidAt(tc.now); got != tc.valid {
			t.Fatalf("ValidAt(%d)=%d, want %d", tc.now, got, tc.valid)
		}
	}
}

func TestTicketDuplicateAndReplace(t *testing.T) {
	tk := New(100)
	if err := tk.Add("bob", 10); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := tk.Add("bob", 50); err != ErrDuplicateApproval {
		t.Fatalf("active re-approve err=%v, want ErrDuplicateApproval", err)
	}
	if err := tk.Add("bob", 110); err != nil { // 旧批准已失效，以新时刻替换
		t.Fatalf("re-approve after expiry: %v", err)
	}
	if got := tk.ValidAt(150); got != 1 {
		t.Fatalf("ValidAt(150)=%d, want 1", got)
	}
	if got := tk.ValidAt(211); got != 0 {
		t.Fatalf("ValidAt(211)=%d, want 0", got)
	}
	if !tk.HasValid("bob", 209) || tk.HasValid("bob", 210) {
		t.Fatal("HasValid boundary mismatch")
	}
}
