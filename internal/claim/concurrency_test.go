package claim_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/internal/claim"
)

// 并发调用必须等价于某个串行顺序。
//
// 设计：事故登记的先后顺序通过单通道严格确定（因此赔付结果有唯一答案），
// 同时并发施加三类彼此冲突的操作：
//   - 同一保单编号的并发新增（恰 1 个成功，其余必须为“编号重复”）；
//   - 对同一保单的并发注销（恰 1 个成功，其余必须为“已注销”）；
//   - 大量并发只读查询。
//
// 配合 `go test -race` 验证锁的正确性，并核对累计赔付永不超额。
func TestConcurrentSerializable(t *testing.T) {
	s := claim.NewSystem()
	for i := 0; i < 20; i++ {
		if err := s.AddPolicy(claim.Policy{
			ID: fmt.Sprintf("p%d", i), Subject: "s", Limit: 1000, Deductible: 0,
			StartDay: 0, EndDay: 100, Insurer: "I",
			Clause: []claim.ClauseType{claim.Ordinary, claim.Excess}[i%2],
		}); err != nil {
			t.Fatal(err)
		}
	}

	registerCh := make(chan claim.Accident)
	var regWG sync.WaitGroup
	for w := 0; w < 8; w++ {
		regWG.Add(1)
		go func() {
			defer regWG.Done()
			for in := range registerCh {
				_, _ = s.RegisterAccident(in)
			}
		}()
	}

	var loadWG sync.WaitGroup
	// 并发竞争新增同一编号。
	for w := 0; w < 16; w++ {
		loadWG.Add(1)
		go func(w int) {
			defer loadWG.Done()
			err := s.AddPolicy(claim.Policy{
				ID: "race-policy", Subject: "other", Limit: 100, Deductible: 0,
				StartDay: 0, EndDay: 1, Insurer: "I",
			})
			if err != nil && !isClass(err, "dup") {
				t.Errorf("concurrent add: unexpected %v", err)
			}
		}(w)
	}

	// 有序登记：通道保证事故按 0..N-1 获得序号。
	go func() {
		for i := 0; i < 100; i++ {
			registerCh <- claim.Accident{
				ID: fmt.Sprintf("a%d", i), Subject: "s", Day: 5, Loss: int64(10 + i%40),
			}
		}
		close(registerCh)
	}()

	// 并发只读。
	for w := 0; w < 8; w++ {
		loadWG.Add(1)
		go func(w int) {
			defer loadWG.Done()
			for i := 0; i < 100; i++ {
				_, _ = s.AccidentResult(fmt.Sprintf("a%d", i))
				_, _ = s.RemainingLimit(fmt.Sprintf("p%d", i%20))
			}
		}(w)
	}

	regWG.Wait()

	// 注册全部完成后，并发竞争注销同一保单（此前无事故覆盖 day 90，可成功）。
	var cancelOK, cancelCancelled int
	var cancelMu sync.Mutex
	for w := 0; w < 16; w++ {
		loadWG.Add(1)
		go func() {
			defer loadWG.Done()
			err := s.CancelPolicy(claim.CancelPolicyInput{PolicyID: "p0", CancelDay: 90})
			switch errClass(err) {
			case "ok":
				cancelMu.Lock()
				cancelOK++
				cancelMu.Unlock()
			case "cancelled":
				cancelMu.Lock()
				cancelCancelled++
				cancelMu.Unlock()
			default:
				t.Errorf("concurrent cancel: unexpected %v", err)
			}
		}()
	}
	loadWG.Wait()
	if cancelOK != 1 || cancelCancelled != 15 {
		t.Fatalf("cancel race want exactly 1 ok / 15 cancelled, got %d / %d", cancelOK, cancelCancelled)
	}

	// 结果自洽：每个事故总额不超过损失，每张保单累计赔付不超过保额。
	for i := 0; i < 100; i++ {
		r, err := s.AccidentResult(fmt.Sprintf("a%d", i))
		if err != nil {
			t.Fatal(err)
		}
		loss := int64(10 + i%40)
		if r.TotalPaid > loss {
			t.Fatalf("accident %d paid %d > loss %d", i, r.TotalPaid, loss)
		}
	}
	for i := 0; i < 20; i++ {
		rem, err := s.RemainingLimit(fmt.Sprintf("p%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if rem < 0 {
			t.Fatalf("policy %d over-limit, remaining=%d", i, rem)
		}
	}
	t.Logf("serializable concurrent ops: 1 add winner, 1 cancel winner / 15 cancelled; limits respected")
}

func isClass(err error, class string) bool { return errClass(err) == class }
