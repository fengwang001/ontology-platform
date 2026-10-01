package placement

import "strings"

func testLabels(kv ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func mustReject(t testingT, err error, want Reason) *Reject {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %v, got nil", want)
	}
	rj, ok := err.(*Reject)
	if !ok || rj.Code != want {
		t.Fatalf("expected error %v, got %v", want, err)
	}
	return rj
}

type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
}

func setupZones() *Scheduler {
	s := NewScheduler(10)
	_ = s.AddNode("n1", "a")
	_ = s.AddNode("n2", "a")
	_ = s.AddNode("n3", "b")
	return s
}

func joinSorted(ids []string) string { return strings.Join(ids, ",") }
