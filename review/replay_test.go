package review

import (
	"reflect"
	"testing"
	"time"
)

// TestReplayDeterminism：同一操作序列在两个独立实例上重放，
// 得到完全相同的结果（ID、面板、全部留痕）。
func TestReplayDeterminism(t *testing.T) {
	seq := func(s *Service) [][]any {
		var log [][]any
		rec := func(parts ...any) { log = append(log, parts) }

		mustAddReviewer(t, s, 1, "U1", "A")
		mustAddReviewer(t, s, 2, "U2", "A")
		mustAddReviewer(t, s, 3, "U2", "B")
		mustAddReviewer(t, s, 4, "U3", "B")
		mustAddReviewer(t, s, 5, "U3", "A")
		mustAddReviewer(t, s, 6, "U4", "A")
		mustAddApplicant(t, s, 900, "UX")

		id, panel, err := s.CreateReview(clockAt(1), 900, 5, nil)
		rec("create", id, panel, err)
		mustVote(t, s, clockAt(2), id, 1, 1, Approve)
		mustVote(t, s, clockAt(3), id, 2, 1, Approve)
		mustVote(t, s, clockAt(4), id, 3, 1, Approve)
		mustVote(t, s, clockAt(5), id, 4, 1, Oppose)
		mustVote(t, s, clockAt(6), id, 5, 1, Abstain)
		v, _ := s.GetReview(id)
		rec("round1", v.History)

		// 复议中评委 2 被新申请回避。
		mustVote(t, s, clockAt(7), id, 1, 2, Approve)
		if err := s.AcceptRecusalRequest(clockAt(8), 900, 2); err != nil {
			t.Fatal(err)
		}
		sub, err := s.HandleRecusal(clockAt(9), id, 2)
		rec("recuse", sub, err)
		mustVote(t, s, clockAt(10), id, sub, 1, Approve)
		vers, _ := s.Versions(id)
		rec("versions", vers)

		// 重新结算：2,3,sub 赞，4 反，5 弃 -> 3 赞再入复议；
		// 新一幕复议 4 赞 1 反对通过。
		v, _ = s.GetReview(id)
		if v.CurrentRound != 2 {
			t.Fatalf("应再次进入复议: %+v", v.History)
		}
		for k, j := range v.Panel {
			c := Approve
			if k == len(v.Panel)-1 {
				c = Oppose // 5 反对：赞成 4，严格过半通过
			}
			if err := s.Vote(clockAt(20+k), id, j, 2, c); err != nil {
				t.Fatal(err)
			}
		}
		v, _ = s.GetReview(id)
		rec("publicity", v.Status, v.History)

		if err := s.AcceptObjection(v.PublicityEnd.Add(-time.Hour), id); err != nil {
			t.Fatal(err)
		}
		if err := s.AdjudicateObjection(v.PublicityEnd.Add(-30*time.Minute), id, false); err != nil {
			t.Fatal(err)
		}
		st, err := s.Finalize(v.PublicityEnd, id)
		rec("final", st, err)
		recs, _ := s.VoteRecords(id, 0)
		rec("records", recs)
		return log
	}

	s1 := NewService(testEpoch)
	s2 := NewService(testEpoch)
	l1 := seq(s1)
	l2 := seq(s2)
	if !reflect.DeepEqual(l1, l2) {
		t.Fatalf("重放结果不一致:\n%v\nvs\n%v", l1, l2)
	}
}
