package erase

import "testing"

func TestOverdueExaminesReturnedPlusAtMostOne(t *testing.T) {
	for _, total := range []int{1000, 100000} {
		l := New(1, 10)
		subject := 1
		for subject < total {
			if _, err := l.Request(1, subject, 1); err != nil {
				t.Fatalf("request subject %d: %v", subject, err)
			}
			subject++
		}
		if _, err := l.Request(1, total, 100); err != nil {
			t.Fatalf("request future subject: %v", err)
		}

		entries, examined := l.overdueExaminedForTest(10)
		if len(entries) != 0 {
			t.Fatalf("strictly before deadline returned %d, want 0", len(entries))
		}
		if examined != 1 {
			t.Fatalf("before deadline examined %d, want 1", examined)
		}

		entries, examined = l.overdueExaminedForTest(11)
		if len(entries) != total-1 {
			t.Fatalf("total=%d returned %d, want %d", total, len(entries), total-1)
		}
		if examined != len(entries)+1 {
			t.Fatalf("total=%d examined %d, want %d", total, examined, len(entries)+1)
		}
	}
}
