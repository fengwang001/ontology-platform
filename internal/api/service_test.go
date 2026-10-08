package api

import (
	"errors"
	"testing"
)

func codeOf(err error) ErrorCode {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestPutDeleteQueryEndToEnd(t *testing.T) {
	svc := New()

	r1, err := svc.Put("Person", "p1", 10, "", "alice")
	if err != nil || r1.SysVersion != 1 {
		t.Fatalf("put1: %+v %v", r1, err)
	}
	// 凭证格式非法：先于一切语义判定。
	if _, err := svc.Put("Person", "p1", 10, "xyz", "x"); codeOf(err) != CodeInvalidArgument {
		t.Fatalf("bad token code=%v", codeOf(err))
	}
	// 空主键：参数非法。
	if _, err := svc.Put("", "p1", 10, "v1", "x"); codeOf(err) != CodeInvalidArgument {
		t.Fatalf("empty type code=%v", codeOf(err))
	}

	r2, err := svc.Put("Person", "p1", 10, "v1", "alice-2") // 同业务起点覆盖
	if err != nil || r2.SysVersion != 2 {
		t.Fatalf("override: %+v %v", r2, err)
	}
	// 陈旧凭证冲突。
	if _, err := svc.Put("Person", "p1", 20, "v1", "stale"); codeOf(err) != CodeConflict {
		t.Fatalf("conflict code=%v", codeOf(err))
	}
	// 凭证最新但早于最早业务起点 10。
	if _, err := svc.Put("Person", "p1", 9, svc.LatestToken("Person", "p1"), "early"); codeOf(err) != CodeBeforeEarliestBiz {
		t.Fatalf("boundary code=%v", codeOf(err))
	}

	if _, err := svc.Delete("Person", "p1", 30, svc.LatestToken("Person", "p1")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Put("Person", "p1", 50, svc.LatestToken("Person", "p1"), "alice-3"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		sys, biz int64
		status   Status
		payload  string
	}{
		{4, 9, StatusNeverWritten, ""},
		{4, 10, StatusAlive, "alice-2"},
		{4, 30, StatusDeleted, ""},
		{4, 49, StatusDeleted, ""},
		{4, 50, StatusAlive, "alice-3"},
		{1, 10, StatusAlive, "alice"}, // 系统时间回溯见旧值
	}
	for _, tc := range cases {
		got, err := svc.Query("Person", "p1", tc.sys, tc.biz)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("QUERY sys=%d biz=%d -> status=%s ver=%d payload=%q", tc.sys, tc.biz, got.Status, got.SysVersion, got.Payload)
		if got.Status != tc.status || got.Payload != tc.payload {
			t.Fatalf("(%d,%d) got %s/%q want %s/%q", tc.sys, tc.biz, got.Status, got.Payload, tc.status, tc.payload)
		}
	}

	if q, _ := svc.Query("Person", "ghost", 999, 999); q.Status != StatusNeverWritten {
		t.Fatalf("never written got %s", q.Status)
	}

	segs, ok, err := svc.History("Person", "p1", 4)
	if err != nil || !ok || len(segs) != 3 {
		t.Fatalf("history: %+v ok=%v err=%v", segs, ok, err)
	}
	if segs[0].Status != StatusAlive || segs[1].Status != StatusDeleted || segs[2].Status != StatusAlive {
		t.Fatalf("phases: %+v", segs)
	}
}
