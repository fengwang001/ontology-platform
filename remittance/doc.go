// Package remittance 实现跨境汇款的报价锁汇、限额占用与人工合规审核。
//
// 快速上手：
//
//	e := remittance.New(remittance.Config{
//	    QuoteTTLSeconds: 100,   // 报价有效期 T（秒）
//	    ReviewSeconds:   60,    // 人工审核时限 R（秒）
//	    ReviewThreshold: 1000,  // 目标额 >= 该值进入人工审核
//	})
//	e.AddSender("alice", remittance.Limits{Single: 5000, Daily: 20000, Annual: 1_000_000})
//
//	qid, err := e.ApplyQuote(remittance.QuoteRequest{
//	    Sender: "alice", SourceCCY: "USD", TargetCCY: "CNY",
//	    Amount: 100, RatePPM: 7200000, Now: 1000,
//	})
//	res, err := e.Submit(remittance.SubmitRequest{
//	    Sender: "alice", QuoteID: qid, Payee: "bob",
//	    IdemKey: "txn-0001", Now: 1005,
//	})
//	// res.TargetAmount = floor(amount*ratePPM/1e6) —— 收款人入账额
//	// res.Occupied     = ceil (amount*ratePPM/1e6) —— 额度占用额
//
//	switch res.Status {
//	case remittance.StatusSucceeded: // 已出款
//	case remittance.StatusPending:   // 等待 Approve/Reject/Withdraw
//	    _ = e.Approve(res.TransferID, 1065) // 含第 R 秒；晚于即 IllegalState
//	}
//
// 设计与取舍见同目录 DESIGN.md。
package remittance
