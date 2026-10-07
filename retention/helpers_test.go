package retention_test

import (
	"bytes"
	"testing"

	"ontology/retention"
)

func newSvc(now int64) (*retention.Service, *retention.LogicalClock, *retention.AuditLog) {
	clk := retention.NewLogicalClock(now)
	log := retention.NewAuditLog(&bytes.Buffer{})
	return retention.NewService(clk, log), clk, log
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
