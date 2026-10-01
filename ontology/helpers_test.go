package ontology

import (
	"fmt"
	"testing"
)

// judge logs the input, output and the reason for the verdict, so each step
// of the test is auditable from the -v output.
func judge(t *testing.T, name, input string, got, want any, reason string) {
	t.Helper()
	ok := fmt.Sprint(got) == fmt.Sprint(want)
	verdict := "PASS"
	if !ok {
		verdict = "FAIL"
	}
	t.Logf("[%s] %s | input=%s | got=%v want=%v | 判定依据: %s", verdict, name, input, got, want, reason)
	if !ok {
		t.Errorf("%s: got %v, want %v (%s)", name, got, want, reason)
	}
}
