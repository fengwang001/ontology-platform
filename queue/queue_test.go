package queue

import (
	"errors"
	"fmt"
	"testing"

	"ontology/triage"
)

// 各等级生命体征：L1..L4。
var vitalsOf = [5]triage.Vitals{
	{},
	{HR: 135, SBP: 120, SpO2: 96, Consciousness: 'A'}, // m=3 → 1 级
	{HR: 120, SBP: 75, SpO2: 93, Consciousness: 'A'},  // s=5 → 2 级
	{HR: 105, SBP: 95, SpO2: 92, Consciousness: 'V'},  // s=4 → 3 级
	{HR: 110, SBP: 100, SpO2: 94, Consciousness: 'A'}, // s=2 → 4 级
}

func mustQueue(t *testing.T, r1, r2, r3, r4, a int) *Queue {
	t.Helper()
	q, err := NewQueue(r1, r2, r3, r4, a)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	return q
}

func mustRegister(t *testing.T, q *Queue, now int, id string, lv int) *Patient {
	t.Helper()
	p, err := q.Register(now, id, vitalsOf[lv])
	if err != nil {
		t.Fatalf("Register(%s): %v", id, err)
	}
	if p.Level != lv {
		t.Fatalf("Register(%s): Level=%d, want %d", id, p.Level, lv)
	}
	return p
}

func TestRegisterErrors(t *testing.T) {
	q := mustQueue(t, 5, 15, 30, 60, 5)
	if _, err := q.Register(0, "a", triage.Vitals{HR: 301, SBP: 120, SpO2: 98, Consciousness: 'A'}); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("非法生命体征: err=%v, want ErrInvalidParam", err)
	}
	mustRegister(t, q, 0, "a", 3)
	if _, err := q.Register(1, "a", vitalsOf[3]); !errors.Is(err, ErrExistence) {
		t.Errorf("重复登记: err=%v, want ErrExistence", err)
	}
	if _, err := NewQueue(0, 15, 30, 60, 5); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("R1=0: err=%v, want ErrInvalidParam", err)
	}
	if _, err := NewQueue(5, 15, 30, 60, 10001); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("A=10001: err=%v, want ErrInvalidParam", err)
	}
}

func TestReassessAsymmetricQ(t *testing.T) {
	q := mustQueue(t, 5, 15, 30, 60, 5)
	mustRegister(t, q, 10, "p", 4) // q=10, la=10

	// 升级（4→3）：q 保留 10，la 更新。
	p, err := q.Reassess(20, "p", vitalsOf[3])
	if err != nil {
		t.Fatalf("Reassess 升级: %v", err)
	}
	t.Logf("升级后: q=%d la=%d level=%d 判定依据=升级不吃亏 q 保留", p.Q, p.LA, p.Level)
	if p.Q != 10 || p.LA != 20 || p.Level != 3 {
		t.Errorf("升级: got q=%d la=%d level=%d, want q=10 la=20 level=3", p.Q, p.LA, p.Level)
	}

	// 同级复评（3→3）：q 不变，la 更新。
	if p, err = q.Reassess(25, "p", vitalsOf[3]); err != nil {
		t.Fatalf("Reassess 同级: %v", err)
	}
	if p.Q != 10 || p.LA != 25 || p.Level != 3 {
		t.Errorf("同级: got q=%d la=%d level=%d, want q=10 la=25 level=3", p.Q, p.LA, p.Level)
	}

	// 降级（3→4）：q=now，la 更新。
	if p, err = q.Reassess(30, "p", vitalsOf[4]); err != nil {
		t.Fatalf("Reassess 降级: %v", err)
	}
	t.Logf("降级后: q=%d la=%d level=%d 判定依据=降级重新排队 q=now", p.Q, p.LA, p.Level)
	if p.Q != 30 || p.LA != 30 || p.Level != 4 {
		t.Errorf("降级: got q=%d la=%d level=%d, want q=30 la=30 level=4", p.Q, p.LA, p.Level)
	}
}

func TestReassessState(t *testing.T) {
	q := mustQueue(t, 5, 15, 30, 60, 5)
	if _, err := q.Reassess(0, "ghost", vitalsOf[3]); !errors.Is(err, ErrExistence) {
		t.Errorf("未登记: err=%v, want ErrExistence", err)
	}
	mustRegister(t, q, 0, "a", 3)
	if chosen, _ := q.Select(1, []int{2, 3, 4}); chosen == nil || chosen.ID != "a" {
		t.Fatalf("Select 应叫到 a")
	}
	if _, err := q.Reassess(2, "a", vitalsOf[3]); !errors.Is(err, ErrState) {
		t.Errorf("已叫号者复评: err=%v, want ErrState", err)
	}
}

func TestOverdueBoundary(t *testing.T) {
	q := mustQueue(t, 5, 15, 30, 60, 5)
	p := mustRegister(t, q, 10, "a", 3) // R[3]=30
	if q.Overdue(p, 40) {
		t.Errorf("now-la=30 恰等 R，不应逾期")
	}
	if !q.Overdue(p, 41) {
		t.Errorf("now-la=31 > R，应逾期")
	}
}

func TestSelectSkippedOrder(t *testing.T) {
	q := mustQueue(t, 5, 15, 30, 60, 5)
	mustRegister(t, q, 0, "b", 3)  // la=0
	mustRegister(t, q, 5, "c", 3)  // la=5
	mustRegister(t, q, 10, "d", 4) // la=10
	// t=36：b(36>30)逾期、c(31>30)逾期、d(26<=60)未逾期。
	chosen, skipped := q.Select(36, []int{2, 3, 4})
	var got []string
	for _, p := range skipped {
		got = append(got, p.ID)
	}
	t.Logf("called=%v skipped=%v 判定依据=逾期者按排序键依序跳过", chosen.ID, got)
	if chosen.ID != "d" || len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Errorf("got called=%s skipped=%v, want called=d skipped=[b c]", chosen.ID, got)
	}
	if chosen.State != Called || chosen.CallAt != 36 {
		t.Errorf("被叫者状态: state=%v callAt=%d, want Called/36", chosen.State, chosen.CallAt)
	}
	// 被跳过者仍候诊，复评后可再被叫到。
	if p, _ := q.Get("b"); p.State != Waiting {
		t.Errorf("b 应仍在候诊, state=%v", p.State)
	}
	if _, err := q.Reassess(37, "b", vitalsOf[3]); err != nil {
		t.Fatalf("b 复评: %v", err)
	}
	if chosen, _ := q.Select(38, []int{2, 3, 4}); chosen == nil || chosen.ID != "b" {
		t.Errorf("b 复评后应最先被叫到（q=0 最小）, got %v", chosen)
	}
}

func TestSelectNoCallable(t *testing.T) {
	q := mustQueue(t, 5, 15, 30, 60, 5)
	if chosen, skipped := q.Select(0, []int{1, 2}); chosen != nil || skipped != nil {
		t.Errorf("空队列: got %v %v", chosen, skipped)
	}
	mustRegister(t, q, 0, "a", 3)
	// 唯一的 3 级且已逾期时，诊室候选无可叫者。
	if chosen, skipped := q.Select(31, []int{2, 3, 4}); chosen != nil || len(skipped) != 1 {
		t.Errorf("全逾期: got chosen=%v skipped=%v", chosen, skipped)
	}
}

func TestSettleMiss(t *testing.T) {
	q := mustQueue(t, 5, 15, 30, 60, 5)
	mustRegister(t, q, 0, "a", 3)
	if chosen, _ := q.Select(10, []int{2, 3, 4}); chosen == nil {
		t.Fatal("应叫到 a")
	}
	// 恰等 A=5 不过号。
	if landed := q.Settle(15); len(landed) != 0 {
		t.Errorf("now-callAt=5 恰等 A，不应过号, landed=%v", landed)
	}
	// 超过 A 落地：q=callAt+A=15，la 与等级不变，miss=1。
	landed := q.Settle(16)
	if len(landed) != 1 || landed[0].ID != "a" {
		t.Fatalf("landed=%v, want [a]", landed)
	}
	p, _ := q.Get("a")
	t.Logf("过号落地: q=%d la=%d level=%d miss=%d 判定依据=q 取 callAt+A 而非 now", p.Q, p.LA, p.Level, p.Miss)
	if p.State != Waiting || p.Q != 15 || p.LA != 0 || p.Level != 3 || p.Miss != 1 {
		t.Errorf("got state=%v q=%d la=%d level=%d miss=%d", p.State, p.Q, p.LA, p.Level, p.Miss)
	}
}

func TestMissThriceGone(t *testing.T) {
	q := mustQueue(t, 5, 15, 30, 60, 5)
	mustRegister(t, q, 0, "a", 3)
	for i := 1; i <= 3; i++ {
		now := i * 10
		if chosen, _ := q.Select(now, []int{2, 3, 4}); chosen == nil || chosen.ID != "a" {
			t.Fatalf("第 %d 次叫号应叫到 a", i)
		}
		landed := q.Settle(now + 6) // now-callAt=6 > A=5
		if len(landed) != 1 {
			t.Fatalf("第 %d 次落地: landed=%v", i, landed)
		}
		p, _ := q.Get("a")
		t.Logf("第 %d 次过号: miss=%d state=%v", i, p.Miss, p.State)
	}
	p, _ := q.Get("a")
	if p.Miss != 3 || p.State != Gone {
		t.Errorf("三次过号应离队: miss=%d state=%v", p.Miss, p.State)
	}
	if chosen, _ := q.Select(100, []int{1, 2, 3, 4}); chosen != nil {
		t.Errorf("离队者不应再被叫到, got %v", chosen.ID)
	}
}

// TestExaminedBounds 验证考察计数与候诊总数无关：100 与 10000 两档对照。
func TestExaminedBounds(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			q := mustQueue(t, 5, 15, 30, 60, 5)
			// 前 3 名候诊者逾期，其余不逾期；另有一半已叫号。
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("w%d", i)
				mustRegister(t, q, 0, id, 3)
				if i >= 3 {
					if _, err := q.Reassess(50, id, vitalsOf[3]); err != nil { // la=50 不逾期
						t.Fatal(err)
					}
				}
			}
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("c%d", i)
				mustRegister(t, q, 60, id, 4)
			}
			// 逐个叫号 n 名 4 级患者（其中 2 名将过号）。
			called := 0
			for {
				chosen, _ := q.Select(60, []int{4})
				if chosen == nil {
					break
				}
				called++
				if called == 1 || called == 2 {
					// 保持 Called，等待过号
				} else {
					if err := q.Arrive(chosen.ID); err != nil {
						t.Fatal(err)
					}
					if err := q.Finish(chosen.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			// 一次成功 Call：考察数 = Skipped+1 = 4，与 n 无关。
			chosen, skipped := q.Select(61, []int{2, 3, 4})
			if chosen == nil {
				t.Fatal("应叫到第 4 名候诊者")
			}
			t.Logf("n=%d Call: examined=%d skipped=%d 判定依据=examined<=skipped+1", n, q.examinedCall, len(skipped))
			if q.examinedCall != len(skipped)+1 || q.examinedCall > 4 {
				t.Errorf("n=%d: examinedCall=%d, want %d", n, q.examinedCall, len(skipped)+1)
			}
			// 一次落地：2 名留置的已叫号者 + 上次 Call 叫到者共 3 人过号，
			// 取出数 <= 过号数+1，与 n 无关。
			landed := q.Settle(100)
			t.Logf("n=%d Settle: examined=%d landed=%d 判定依据=examined<=landed+1", n, q.examinedSettle, len(landed))
			if len(landed) != 3 || q.examinedSettle > len(landed)+1 {
				t.Errorf("n=%d: examinedSettle=%d landed=%d", n, q.examinedSettle, len(landed))
			}
		})
	}
}
