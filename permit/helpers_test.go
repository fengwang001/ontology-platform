package permit

import (
	"fmt"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func logsprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
