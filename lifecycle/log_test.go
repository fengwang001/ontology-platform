package lifecycle

import (
	"errors"
	"testing"
	"time"
)

// 每次操作都在日志中记录输入、输出与据以判定的状态和时刻；
// 被拒绝的操作同样有记录，且状态前后一致、时刻取自系统时钟。
func TestOperationLogContents(t *testing.T) {
	clock := NewFakeClock(t0)
	lg := NewSliceLogger()
	svc := NewService(clock, NewMemoryAuditLog(), lg)
	if err := svc.CreateObject("o", Attrs{"k": "v"}); err != nil {
		t.Fatal(err)
	}

	clock.Set(t0.Add(time.Second))
	if err := svc.Freeze("o", IdentityAdmin, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := svc.Archive("o", IdentityUser); !errors.Is(err, ErrFrozenNotExpired) {
		t.Fatalf("archive = %v", err)
	}
	clock.Set(t0.Add(11 * time.Second))
	if err := svc.Archive("o", IdentityAdmin); err != nil {
		t.Fatal(err)
	}

	entries := lg.Entries()
	if len(entries) < 4 {
		t.Fatalf("log entries = %d, want >= 4", len(entries))
	}

	// 建对象：记录 after=alive。
	if entries[0].Operation != "create" || entries[0].StateAfter != StateAlive ||
		!entries[0].Time.Equal(t0) || entries[0].Output != "accepted" {
		t.Fatalf("bad create entry: %+v", entries[0])
	}

	// 冻结：记录入参时长、前后状态、判定时刻。
	freeze := entries[1]
	if freeze.Operation != "freeze" || freeze.StateBefore != StateAlive ||
		freeze.StateAfter != StateFrozen || freeze.Input == "" ||
		!freeze.Time.Equal(t0.Add(time.Second)) {
		t.Fatalf("bad freeze entry: %+v", freeze)
	}

	// 被拒绝的提前归档：记录错误类别，且状态未变、时刻正确。
	rejected := entries[2]
	if rejected.Operation != "archive" || rejected.Output != "rejected" ||
		rejected.Err != ErrFrozenNotExpired.Error() ||
		rejected.StateBefore != StateFrozen || rejected.StateAfter != StateFrozen ||
		!rejected.Time.Equal(t0.Add(time.Second)) {
		t.Fatalf("bad rejected entry: %+v", rejected)
	}

	// 到期归档放行。
	accepted := entries[3]
	if accepted.Output != "accepted" || accepted.StateBefore != StateFrozen ||
		accepted.StateAfter != StateArchived ||
		!accepted.Time.Equal(t0.Add(11*time.Second)) {
		t.Fatalf("bad accepted archive entry: %+v", accepted)
	}
}

// 不存在对象的操作也要留日志，且不产生任何状态转换审计记录。
func TestLogRecordsMissingObject(t *testing.T) {
	clock := NewFakeClock(t0)
	lg := NewSliceLogger()
	mem := NewMemoryAuditLog()
	svc := NewService(clock, mem, lg)

	err := svc.Undo("ghost", IdentityAdmin)
	if !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("err = %v", err)
	}
	if len(lg.Entries()) != 1 {
		t.Fatalf("log entries = %d, want 1", len(lg.Entries()))
	}
	e := lg.Entries()[0]
	if e.Err != ErrObjectNotFound.Error() || e.Output != "rejected" || e.ObjectID != "ghost" {
		t.Fatalf("bad entry: %+v", e)
	}
	if len(mem.All()) != 0 {
		t.Fatalf("audit records for missing object = %d, want 0", len(mem.All()))
	}
}
