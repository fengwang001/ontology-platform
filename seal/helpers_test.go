package seal

import (
	"os"
	"testing"
)

// testPolicy fixes the numeric landscape used across all tests.
//
//	approval tiers:        amount < 1000 -> 1 approver
//	                       1000 <= amount < 10000 -> 2 approvers
//	                       10000 <= amount -> 3 approvers incl. a custodian
//	dual-presence:        amount >= 5000 needs two distinct custodians
//	approval validity:    100 seconds after full approval
//	receipt window:       200 seconds after execution
func testPolicy() Policy {
	return Policy{Tier1: 1000, Tier2: 10000, DualPresence: 5000, ApprovalValidity: 100, ReceiptWindow: 200}
}

func newTestService(t *testing.T) (*Service, *SliceLogger) {
	t.Helper()
	logger := &SliceLogger{}
	return NewService(testPolicy(), logger), logger
}

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", ctx, err)
	}
}

func errCode(err error) ErrorCode {
	if err == nil {
		return ""
	}
	if se, ok := err.(*SealError); ok {
		return se.Code
	}
	return "NON_SEAL_ERROR"
}

func mustFail(t *testing.T, err error, want ErrorCode, ctx string) {
	t.Helper()
	if got := errCode(err); got != want {
		t.Fatalf("%s: want %s, got %v", ctx, want, err)
	}
}

func setupWorld(t *testing.T, s *Service) {
	t.Helper()
	// seal S with custodians C1/C2.
	mustOK(t, s.CreateSeal(0, "S", "contract", "C1", "C2"), "create seal")
	// broad grant for employee E on seal S.
	mustOK(t, s.GrantAuthorization(0, Grant{
		ID: "G", Employee: "E", SealID: "S",
		Materials:  map[string]struct{}{"M1": {}, "M2": {}},
		AmountCap:  20000,
		DailyCap:   3,
		ValidFrom:  100,
		ValidUntil: 100000,
	}), "grant")
}

// dumpLogs prints every step's input/output/basis when SEAL_TEST_VERBOSE=1.
func dumpLogs(t *testing.T, logger *SliceLogger) {
	t.Helper()
	if os.Getenv("SEAL_TEST_VERBOSE") == "" {
		return
	}
	for _, e := range logger.Entries {
		t.Logf("[%d] t=%d %s | %s => %s | %s", e.Seq, e.Now, e.Op, e.Input, e.Output, e.Basis)
	}
}
