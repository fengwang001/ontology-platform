package contract

import "strings"

import "testing"

// TestOperationLog 验证日志记录每步输入、输出与判定依据。
func TestOperationLog(t *testing.T) {
	s := NewService()
	s.SetLogging(true)
	mustOK(t, s.CreateContract(baseContract("LOG", 0, 0, 100)), "create")
	mustOK(t, s.AddAmendment(AddAmendmentInput{
		ContractID: "LOG", AmendmentID: "M1", Now: 1, EffectiveDay: 10,
		Changes: map[string]int{"PRICE": 200}}), "add")
	wantCode(t, s.Sign("LOG", "M1", "A", 0), ErrClockRollback, "rollback rejected")
	mustOK(t, s.Sign("LOG", "M1", "A", 2), "a")
	mustOK(t, s.Sign("LOG", "M1", "B", 3), "b")

	log := s.SnapshotLog()
	for _, want := range []string{"CreateContract", "AddAmendment", "Sign", "OK", "签署完成日"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
	// 被拒绝的操作不产生成功日志条目（只记录被接受操作）。
	if strings.Contains(log, "party=A now=0") {
		t.Fatalf("rejected op must not be logged:\n%s", log)
	}
}
