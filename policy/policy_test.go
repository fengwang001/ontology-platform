package policy

import "testing"

// TestDecideMatrix 对 Running/已结束/不存在三类现状核验九种策略组合的纯判定。
func TestDecideMatrix(t *testing.T) {
	conflicts := []ConflictPolicy{Fail, UseExisting, Terminate}
	reuses := []ReusePolicy{AllowAll, AllowFailedOnly, Reject}
	ended := []State{Completed, Failed, Cancelled, Terminated}

	// Running：只看 conflict，与 reuse 无关（Terminate 绕过 reuse）。
	for _, c := range conflicts {
		for _, r := range reuses {
			got := Decide(true, Existing{Run: 7, State: Running}, r, c)
			want := map[ConflictPolicy]Verdict{
				Fail: VerdictRejectRunning, UseExisting: VerdictUseExisting, Terminate: VerdictTerminateOK,
			}[c]
			t.Logf("input: exists=true state=Running reuse=%d conflict=%d -> output=%d want=%d 依据: Running 只看 conflict",
				r, c, got, want)
			if got != want {
				t.Fatalf("Running reuse=%d conflict=%d: got %d want %d", r, c, got, want)
			}
		}
	}

	// 已结束：只看 reuse，与 conflict 无关。
	for _, s := range ended {
		for _, c := range conflicts {
			for _, r := range reuses {
				got := Decide(true, Existing{Run: 7, State: s}, r, c)
				want := VerdictRejectReuse
				if r == AllowAll {
					want = VerdictReplace
				} else if r == AllowFailedOnly && s != Completed {
					want = VerdictReplace
				}
				t.Logf("input: exists=true state=%d reuse=%d conflict=%d -> output=%d want=%d 依据: 已结束只看 reuse",
					s, r, c, got, want)
				if got != want {
					t.Fatalf("state=%d reuse=%d conflict=%d: got %d want %d", s, r, c, got, want)
				}
			}
		}
	}

	// 不存在：一律 New（容量由调用方负责）。
	for _, c := range conflicts {
		for _, r := range reuses {
			if got := Decide(false, Existing{}, r, c); got != VerdictNew {
				t.Fatalf("absent reuse=%d conflict=%d: got %d want VerdictNew", r, c, got)
			}
		}
	}
}

func TestValidators(t *testing.T) {
	if !ValidReuse(AllowAll) || ValidReuse(ReusePolicy(99)) {
		t.Fatal("ValidReuse 越界判定错误")
	}
	if !ValidConflict(Terminate) || ValidConflict(ConflictPolicy(-1)) {
		t.Fatal("ValidConflict 越界判定错误")
	}
	if !ValidFinishState(Completed) || !ValidFinishState(Cancelled) {
		t.Fatal("合法 Finish 状态被拒")
	}
	if ValidFinishState(Running) || ValidFinishState(Terminated) {
		t.Fatal("Running/Terminated 不应是合法 Finish 状态")
	}
}
