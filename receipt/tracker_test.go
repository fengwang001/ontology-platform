package receipt

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, s int) *Tracker {
	t.Helper()
	tr, err := NewTracker(s)
	if err != nil {
		t.Fatalf("NewTracker(%d): %v", s, err)
	}
	return tr
}

func mustSend(t *testing.T, tr *Tracker, msg string, rcpts []string, deadline int64) {
	t.Helper()
	bs := make([][]byte, len(rcpts))
	for i, r := range rcpts {
		bs[i] = []byte(r)
	}
	if err := tr.Send([]byte(msg), bs, deadline); err != nil {
		t.Fatalf("Send(%s): %v", msg, err)
	}
}

func recv(t *testing.T, tr *Tracker, msg, rcpt, kind string, attempt int, ts int64) ReceiptOutcome {
	t.Helper()
	out, err := tr.Receipt([]byte(msg), []byte(rcpt), kind, attempt, ts)
	if err != nil {
		t.Fatalf("Receipt(%s,%s,%s,%d): unexpected error %v", msg, rcpt, kind, attempt, err)
	}
	return out
}

func wantOut(t *testing.T, got, want ReceiptOutcome, ctx string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %s, want %s", ctx, got, want)
	}
}

func wantErr(t *testing.T, got, want error, ctx string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: got error %v, want %v", ctx, got, want)
	}
}

func status(t *testing.T, tr *Tracker, msg string) MessageStatus {
	t.Helper()
	st, err := tr.Status([]byte(msg))
	if err != nil {
		t.Fatalf("Status(%s): %v", msg, err)
	}
	return st
}

// 题目给出的完整示例。
func TestWorkedExample(t *testing.T) {
	tr := mustNew(t, 2)
	mustSend(t, tr, "m", []string{"A", "B", "C"}, 100)

	wantOut(t, recv(t, tr, "m", "A", "soft", 1, 10), Applied, "A soft1")
	wantOut(t, recv(t, tr, "m", "A", "sent", 2, 20), Applied, "A sent2")
	wantOut(t, recv(t, tr, "m", "A", "soft", 2, 30), Applied, "A soft2")
	wantOut(t, recv(t, tr, "m", "A", "soft", 3, 40), Applied, "A soft3")
	if st := status(t, tr, "m"); st.Rcpts["A"].Fail != FailSoft || st.Rcpts["A"].R != 1 {
		t.Fatalf("A = %+v, want soft/r=1 (sent#2 advanced r before soft failed)", st.Rcpts["A"])
	}

	wantOut(t, recv(t, tr, "m", "B", "delivered", 1, 50), Applied, "B delivered1")
	wantOut(t, recv(t, tr, "m", "B", "hard", 1, 60), Stale, "B hard1 late")
	wantOut(t, recv(t, tr, "m", "B", "sent", 1, 70), Stale, "B sent1 stale")
	if st := status(t, tr, "m"); !st.Rcpts["B"].Late || st.Rcpts["B"].R != 2 {
		t.Fatalf("B = %+v, want r=2 late=true", st.Rcpts["B"])
	}

	expired, err := tr.Tick(100)
	if err != nil || len(expired) != 1 || expired[0] != [2]string{"m", "C"} {
		t.Fatalf("Tick(100) = %v, %v; want [[m C]]", expired, err)
	}
	if st := status(t, tr, "m"); st.Summary != "partial" || st.Rcpts["C"].Fail != FailExpired {
		t.Fatalf("after tick: summary=%s C=%+v, want partial/exp", st.Summary, st.Rcpts["C"])
	}

	// exp 后 delivered ts=99 撤销；汇总仍 partial（A soft）。
	wantOut(t, recv(t, tr, "m", "C", "delivered", 1, 99), Applied, "C delivered ts99 revoke")
	st := status(t, tr, "m")
	if st.Rcpts["C"].Fail != FailNone || st.Rcpts["C"].R != 2 || st.Summary != "partial" {
		t.Fatalf("after revoke: %+v summary=%s", st.Rcpts["C"], st.Summary)
	}
}

// 到期撤销边界：ts==deadline 不撤销，ts=deadline-1 撤销；exp 后 sent 不撤销。
func TestExpiryRevokeBoundary(t *testing.T) {
	tr := mustNew(t, 1)
	mustSend(t, tr, "e", []string{"E", "F"}, 100)
	if expired, _ := tr.Tick(100); len(expired) != 2 {
		t.Fatalf("Tick = %v, want 2 expired", expired)
	}
	// 重复 Tick 不重复返回。
	if expired, _ := tr.Tick(100); len(expired) != 0 {
		t.Fatalf("repeat Tick = %v, want empty", expired)
	}

	wantOut(t, recv(t, tr, "e", "E", "delivered", 1, 100), Ignored, "E ts==deadline")
	if st := status(t, tr, "e"); st.Rcpts["E"].Fail != FailExpired {
		t.Fatal("E must remain exp at ts == deadline")
	}
	wantOut(t, recv(t, tr, "e", "F", "read", 1, 99), Applied, "F read ts99")
	if st := status(t, tr, "e"); st.Rcpts["F"].Fail != FailNone || st.Rcpts["F"].R != 3 {
		t.Fatalf("F = %+v, want revoked r=3", st.Rcpts["F"])
	}

	tr2 := mustNew(t, 1)
	mustSend(t, tr2, "e2", []string{"G"}, 10)
	_, _ = tr2.Tick(10) // deadline 恰等于 now 即到期
	wantOut(t, recv(t, tr2, "e2", "G", "sent", 1, 5), Ignored, "G sent after exp")
	if st := status(t, tr2, "e2"); st.Rcpts["G"].Fail != FailExpired || st.Rcpts["G"].R != 0 {
		t.Fatalf("G = %+v, sent must not revoke exp", st.Rcpts["G"])
	}
}

// 软退信作废与阈值：cnt 恰为 S 失败、差 1 不失败；后到 sent 作废旧 soft。
func TestSoftInvalidationAndThreshold(t *testing.T) {
	tr := mustNew(t, 2)
	mustSend(t, tr, "s", []string{"A", "B", "C"}, 1000)

	// sent 先于 soft：不作废，cnt 差 1 不失败、恰等于 S 失败。
	wantOut(t, recv(t, tr, "s", "A", "sent", 1, 0), Applied, "A sent1")
	wantOut(t, recv(t, tr, "s", "A", "soft", 1, 1), Applied, "A soft1 cnt=1")
	if st := status(t, tr, "s"); st.Rcpts["A"].Fail != FailNone {
		t.Fatal("A cnt=1 < S must stay undecided")
	}
	wantOut(t, recv(t, tr, "s", "A", "soft", 2, 2), Applied, "A soft2 cnt=2")
	if st := status(t, tr, "s"); st.Rcpts["A"].Fail != FailSoft {
		t.Fatal("A cnt == S must be soft")
	}
	// soft 终态后再来回执一律 Ignored/Duplicate。
	wantOut(t, recv(t, tr, "s", "A", "delivered", 1, 0), Ignored, "A delivered after soft")

	// soft#1 后到 sent#3：soft#1 作废；soft#3 cnt=1；soft#4 cnt=2 才失败。
	wantOut(t, recv(t, tr, "s", "B", "soft", 1, 0), Applied, "B soft1")
	wantOut(t, recv(t, tr, "s", "B", "sent", 3, 0), Applied, "B sent3")
	wantOut(t, recv(t, tr, "s", "B", "soft", 3, 0), Applied, "B soft3 cnt=1")
	if st := status(t, tr, "s"); st.Rcpts["B"].Fail != FailNone {
		t.Fatalf("B cnt=1 must not fail, got %v", st.Rcpts["B"].Fail)
	}
	wantOut(t, recv(t, tr, "s", "B", "soft", 4, 0), Applied, "B soft4 cnt=2")
	if st := status(t, tr, "s"); st.Rcpts["B"].Fail != FailSoft {
		t.Fatal("B must fail at cnt=2")
	}

	// soft 在 r>=2 时 Stale，不计入 softSet。
	wantOut(t, recv(t, tr, "s", "C", "delivered", 1, 0), Applied, "C delivered")
	wantOut(t, recv(t, tr, "s", "C", "soft", 1, 0), Stale, "C soft stale")
	if st := status(t, tr, "s"); st.Rcpts["C"].Fail != FailNone || st.Rcpts["C"].R != 2 {
		t.Fatalf("C = %+v want r=2 fail=none", st.Rcpts["C"])
	}
}

// 乱序 sent 晚于 delivered：结果 Stale，但白盒检查 sentA 仍被推进。
func TestLateSentStaleButAdvancesSentA(t *testing.T) {
	tr := mustNew(t, 2)
	mustSend(t, tr, "o", []string{"A"}, 1000)
	wantOut(t, recv(t, tr, "o", "A", "delivered", 1, 0), Applied, "A delivered")
	wantOut(t, recv(t, tr, "o", "A", "sent", 7, 0), Stale, "A late sent")
	if got := tr.msgs["o"].rcpts["A"].sentA; got != 7 {
		t.Fatalf("sentA = %d, want 7 even when sent is Stale", got)
	}
}

// hard 在 r=1 置 hard；r=2 为 Stale+late；exp 后 hard 升级、soft Ignored。
func TestHardProgression(t *testing.T) {
	tr := mustNew(t, 3)
	mustSend(t, tr, "h", []string{"R1", "R2", "E"}, 10)
	wantOut(t, recv(t, tr, "h", "R1", "sent", 1, 0), Applied, "R1 sent")
	wantOut(t, recv(t, tr, "h", "R1", "hard", 1, 0), Applied, "R1 hard at r=1")
	wantOut(t, recv(t, tr, "h", "R1", "delivered", 2, 0), Ignored, "R1 delivered after hard")
	wantOut(t, recv(t, tr, "h", "R1", "hard", 2, 0), Ignored, "R1 second hard")

	wantOut(t, recv(t, tr, "h", "R2", "delivered", 1, 0), Applied, "R2 delivered")
	wantOut(t, recv(t, tr, "h", "R2", "hard", 1, 0), Stale, "R2 hard at r=2")
	if st := status(t, tr, "h"); st.Rcpts["R2"].Fail != FailNone || !st.Rcpts["R2"].Late {
		t.Fatalf("R2 = %+v", st.Rcpts["R2"])
	}

	_, _ = tr.Tick(10)
	wantOut(t, recv(t, tr, "h", "E", "soft", 1, 5), Ignored, "E soft after exp")
	wantOut(t, recv(t, tr, "h", "E", "hard", 1, 5), Applied, "E hard upgrades exp")
	if st := status(t, tr, "h"); st.Rcpts["E"].Fail != FailHard {
		t.Fatalf("E = %+v want hard", st.Rcpts["E"])
	}
}

// Duplicate 不改任何状态，即使 ts 不同。
func TestDuplicate(t *testing.T) {
	tr := mustNew(t, 2)
	mustSend(t, tr, "d", []string{"A"}, 100)
	wantOut(t, recv(t, tr, "d", "A", "sent", 1, 0), Applied, "sent1")
	wantOut(t, recv(t, tr, "d", "A", "sent", 1, 99), Duplicate, "sent1 dup other ts")
	wantOut(t, recv(t, tr, "d", "A", "delivered", 1, 5), Applied, "delivered1")
	wantOut(t, recv(t, tr, "d", "A", "delivered", 1, 6), Duplicate, "delivered1 dup")
	wantOut(t, recv(t, tr, "d", "A", "read", 1, 7), Applied, "read1")
	wantOut(t, recv(t, tr, "d", "A", "read", 1, 8), Duplicate, "read1 dup")
	st := status(t, tr, "d")
	if st.Rcpts["A"].R != 3 || st.Rcpts["A"].Fail != FailNone {
		t.Fatalf("A = %+v", st.Rcpts["A"])
	}
}

// 汇总口径：failed / inflight / partial / delivered / read 各一例。
func TestSummaries(t *testing.T) {
	// failed：全体有失败标记。
	tr := mustNew(t, 1)
	mustSend(t, tr, "f", []string{"A", "B"}, 100)
	wantOut(t, recv(t, tr, "f", "A", "hard", 1, 0), Applied, "fA hard")
	_, _ = tr.Tick(100)
	if st := status(t, tr, "f"); st.Summary != "failed" {
		t.Fatalf("summary=%s want failed", st.Summary)
	}

	// inflight：仍有 pending。
	tr = mustNew(t, 1)
	mustSend(t, tr, "i", []string{"A", "B", "C"}, 1000)
	wantOut(t, recv(t, tr, "i", "A", "hard", 1, 0), Applied, "iA hard")
	wantOut(t, recv(t, tr, "i", "B", "read", 1, 0), Applied, "iB read")
	// C 无任何回执：pending。
	if st := status(t, tr, "i"); st.Summary != "inflight" {
		t.Fatalf("summary=%s want inflight", st.Summary)
	}

	// partial：全部已决且有失败。
	wantOut(t, recv(t, tr, "i", "C", "delivered", 1, 0), Applied, "iC delivered")
	if st := status(t, tr, "i"); st.Summary != "partial" {
		t.Fatalf("summary=%s want partial", st.Summary)
	}

	// delivered：全体 r>=2 且无失败，但并非全体 read。
	tr = mustNew(t, 1)
	mustSend(t, tr, "dl", []string{"A", "B"}, 1000)
	wantOut(t, recv(t, tr, "dl", "A", "delivered", 1, 0), Applied, "dlA")
	wantOut(t, recv(t, tr, "dl", "B", "read", 1, 0), Applied, "dlB")
	if st := status(t, tr, "dl"); st.Summary != "delivered" {
		t.Fatalf("summary=%s want delivered", st.Summary)
	}

	// read：全体 r==3 且无失败。
	tr = mustNew(t, 1)
	mustSend(t, tr, "rd", []string{"A", "B"}, 1000)
	wantOut(t, recv(t, tr, "rd", "A", "read", 1, 0), Applied, "rdA")
	wantOut(t, recv(t, tr, "rd", "B", "read", 1, 0), Applied, "rdB")
	if st := status(t, tr, "rd"); st.Summary != "read" {
		t.Fatalf("summary=%s want read", st.Summary)
	}
}

// 拒绝原因与优先级；被拒绝操作不得改任何状态。
func TestRejections(t *testing.T) {
	// NewTracker：S 越界。
	if _, err := NewTracker(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewTracker(0) err=%v", err)
	}
	if _, err := NewTracker(17); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewTracker(17) err=%v", err)
	}

	tr := mustNew(t, 2)
	// Send 参数非法优先于消息已存在。
	if err := tr.Send(nil, [][]byte{[]byte("A")}, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty msg err=%v", err)
	}
	if err := tr.Send([]byte("x"), nil, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("no rcpts err=%v", err)
	}
	if err := tr.Send([]byte("x"), [][]byte{{}, {}}, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty/dup rcpts err=%v", err)
	}
	if err := tr.Send([]byte("x"), [][]byte{[]byte("A")}, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad deadline err=%v", err)
	}
	mustSend(t, tr, "x", []string{"A"}, 100)
	wantErr(t, tr.Send([]byte("x"), [][]byte{[]byte("A")}, 100), ErrMessageExists, "dup send")

	// Receipt：参数非法 > 消息不存在 > 收件人不属于。
	_, err := tr.Receipt([]byte("x"), []byte("A"), "bogus", 1, 0)
	wantErr(t, err, ErrInvalidArgument, "bad kind")
	_, err = tr.Receipt([]byte("x"), []byte("A"), "sent", 0, 0)
	wantErr(t, err, ErrInvalidArgument, "bad attempt")
	_, err = tr.Receipt([]byte("x"), []byte("A"), "sent", 17, 0)
	wantErr(t, err, ErrInvalidArgument, "attempt 17")
	_, err = tr.Receipt([]byte("x"), []byte("A"), "sent", 1, maxTS+1)
	wantErr(t, err, ErrInvalidArgument, "bad ts")
	_, err = tr.Receipt([]byte("nope"), []byte("A"), "sent", 1, 0)
	wantErr(t, err, ErrMessageNotFound, "missing msg")
	_, err = tr.Receipt([]byte("x"), []byte("Z"), "sent", 1, 0)
	wantErr(t, err, ErrUnknownRcpt, "unknown rcpt")

	// 被拒的 Receipt 不进 seen：参数非法的回执修正后仍应 Applied。
	// （消息不存在/收件人不属于也不应留下痕迹。）
	_, err = tr.Receipt([]byte("x"), []byte("Z"), "sent", 9, 0)
	_ = err
	wantOut(t, recv(t, tr, "x", "A", "sent", 9, 0), Applied, "rejected-before did not consume seen")

	// Tick：参数非法优先于时钟回退；时钟回退不改时钟。
	if _, err := tr.Tick(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("tick negative err=%v", err)
	}
	if _, err := tr.Tick(maxTS + 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("tick too large err=%v", err)
	}
	if _, err := tr.Tick(50); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Tick(49); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("clock skew err=%v", err)
	}
	if _, err := tr.Tick(50); err != nil {
		t.Fatalf("clock unchanged after skew: %v", err)
	}

	// Status：空 msg 为参数非法（优先于不存在），不存在报 MessageNotFound。
	_, err = tr.Status(nil)
	wantErr(t, err, ErrInvalidArgument, "status empty")
	_, err = tr.Status([]byte("nope"))
	wantErr(t, err, ErrMessageNotFound, "status missing")
}
