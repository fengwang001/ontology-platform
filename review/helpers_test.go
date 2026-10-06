package review

import (
	"testing"
	"time"
)

var testEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func clockAt(minute int) time.Time { return testEpoch.Add(time.Duration(minute) * time.Minute) }

func mustAddReviewer(t *testing.T, s *Service, id int, unit, group string) {
	t.Helper()
	if err := s.AddReviewer(Reviewer{ID: id, Unit: unit, Group: group}); err != nil {
		t.Fatalf("AddReviewer(%d): %v", id, err)
	}
}

func mustAddApplicant(t *testing.T, s *Service, id int, unit string) {
	t.Helper()
	if err := s.AddApplicant(Applicant{ID: id, Unit: unit}); err != nil {
		t.Fatalf("AddApplicant(%d): %v", id, err)
	}
}

func mustVote(t *testing.T, s *Service, at time.Time, rid, judge, round int, c Choice) {
	t.Helper()
	if err := s.Vote(at, rid, judge, round, c); err != nil {
		t.Fatalf("Vote(评审=%d 评委=%d 轮=%d): %v", rid, judge, round, err)
	}
}

func mustCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	var e *Error
	if err == nil {
		t.Fatalf("期望错误 %s，实际成功", codeName(want))
	}
	if !asError(err, &e) || e.Code != want {
		t.Fatalf("期望错误 %s，实际 %v", codeName(want), err)
	}
}

func asError(err error, target **Error) bool {
	e, ok := err.(*Error)
	if !ok {
		return false
	}
	*target = e
	return true
}

func codeOf(err error) ErrorCode {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return 0
}

// setupSmall 构造 7 名评委（U1/U2 两单位，A/B 两组）与 1 名 U1 申报人。
func setupSmall(t *testing.T) *Service {
	t.Helper()
	s := NewService(testEpoch)
	// 1,2,3 属 A 组 U1；4,5 属 B 组 U2；6 属 A 组 U2；7 属 B 组 U1。
	mustAddReviewer(t, s, 1, "U1", "A")
	mustAddReviewer(t, s, 2, "U1", "A")
	mustAddReviewer(t, s, 3, "U1", "A")
	mustAddReviewer(t, s, 4, "U2", "B")
	mustAddReviewer(t, s, 5, "U2", "B")
	mustAddReviewer(t, s, 6, "U2", "A")
	mustAddReviewer(t, s, 7, "U1", "B")
	mustAddApplicant(t, s, 100, "U1")
	return s
}

// setupLarge 构造 9 名与申报人 300（单位 UX）无同单位关系的评委，
// 编号 1..9，专业组 A/B 交替。
func setupLarge(t *testing.T) *Service {
	t.Helper()
	s := NewService(testEpoch)
	for id := 1; id <= 9; id++ {
		g := "A"
		if id%2 == 0 {
			g = "B"
		}
		mustAddReviewer(t, s, id, "U"+g, g)
	}
	mustAddApplicant(t, s, 300, "UX")
	return s
}
