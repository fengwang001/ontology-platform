package ontology

import "testing"

// tlog 把输入、实际输出与判定依据统一打印到测试日志。
func tlog(t *testing.T, format string, args ...interface{}) {
	t.Helper()
	t.Logf("[CASE] "+format, args...)
}

func newTestEngine(t *testing.T, commits map[string][]string) *Engine {
	t.Helper()
	e := NewEngine()
	for id, files := range commits {
		if err := e.AddCommit(id, files); err != nil {
			t.Fatalf("AddCommit(%s): %v", id, err)
		}
	}
	return e
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func statusName(s PathStatus) string {
	switch s {
	case StatusMaterialized:
		return "MATERIALIZED"
	case StatusExcludedByRule:
		return "EXCLUDED"
	case StatusNoRuleMatch:
		return "NO_RULE_MATCH"
	case StatusEmptyDirectory:
		return "EMPTY_DIRECTORY"
	default:
		return "NOT_IN_COMMIT"
	}
}

func TestSkeletonCompiles(t *testing.T) {
	e := NewEngine()
	tlog(t, "NewEngine => commit=%q version=%d", e.CurrentCommit(), e.CurrentVersion())
	if e.CurrentVersion() != 0 {
		t.Fatalf("initial version = %d, want 0", e.CurrentVersion())
	}
}
