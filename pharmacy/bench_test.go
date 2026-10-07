package pharmacy_test

import (
	"fmt"
	"testing"

	. "ontology/pharmacy"
)

func mustB(b *testing.B, err error) {
	b.Helper()
	if err != nil {
		b.Fatalf("unexpected: %v", err)
	}
}

// addCancelledHistory 向引擎加入 n 张已取消的历史处方（药品 H 的欠药已作废）。
func addCancelledHistory(b *testing.B, e *Engine, now, n int, prefix string) {
	b.Helper()
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s%d", prefix, i)
		mustB(b, e.AcceptPrescription(now, RxInput{ID: id, Patient: "p", IssueTime: 0, Lines: []LineInput{{DrugID: "H", Qty: 1}}}))
		mustB(b, e.CancelPrescription(now, id))
	}
}

// 查询某药品欠药总量：O(1) 计数，开销不随该药历史欠药行数增长。
func BenchmarkDrugBackorderQuery(b *testing.B) {
	for _, scale := range []int{10_000, 300_000} {
		b.Run(fmt.Sprintf("history=%d", scale), func(b *testing.B) {
			e, err := NewEngine(Config{R: 5})
			mustB(b, err)
			mustB(b, e.RegisterDrug(0, "T", 1, true))
			mustB(b, e.RegisterDrug(0, "H", 1, true))
			// scale 张欠药处方随后全部取消：药品 T 的历史欠药行数。
			for i := 0; i < scale; i++ {
				id := fmt.Sprintf("v%d", i)
				mustB(b, e.AcceptPrescription(0, RxInput{ID: id, Patient: "p", IssueTime: 0, Lines: []LineInput{{DrugID: "T", Qty: 5}}}))
				mustB(b, e.CancelPrescription(0, id))
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := e.QueryDrug(0, "T"); err != nil {
					b.Fatalf("QueryDrug: %v", err)
				}
			}
		})
	}
}

// 到货再分配：开销只与当前欠药行数相关，不随历史处方总数增长。
func BenchmarkInboundReallocation(b *testing.B) {
	for _, scale := range []int{10_000, 200_000} {
		b.Run(fmt.Sprintf("history=%d", scale), func(b *testing.B) {
			e, err := NewEngine(Config{R: 100000})
			mustB(b, err)
			mustB(b, e.RegisterDrug(0, "T", 1, true))
			mustB(b, e.RegisterDrug(0, "H", 1, true))
			addCancelledHistory(b, e, 0, scale, "h")
			now := 1
			rxN := 0
			var pending []string
			addBackorders := func(n int) {
				for i := 0; i < n; i++ {
					id := fmt.Sprintf("bo%d", rxN)
					rxN++
					mustB(b, e.AcceptPrescription(now, RxInput{ID: id, Patient: "p", IssueTime: now, Lines: []LineInput{{DrugID: "T", Qty: 10}}}))
					pending = append(pending, id)
				}
			}
			addBackorders(200)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if d, _ := e.QueryDrug(now, "T"); d.BackorderTotal == 0 {
					b.StopTimer()
					for _, id := range pending { // 已满足的欠药处方取消掉，保持状态有界
						mustB(b, e.CancelPrescription(now, id))
					}
					pending = pending[:0]
					addBackorders(200)
					b.StartTimer()
				}
				now++
				mustB(b, e.Inbound(now, "T", 10)) // 恰好满足队首一个欠药行
			}
		})
	}
}

// 失效级联再分配：K 个预留同时失效并分配给 K 个欠药行，
// 开销与历史处方总数无关。
func BenchmarkExpiryCascade(b *testing.B) {
	const k = 500
	for _, scale := range []int{10_000, 200_000} {
		b.Run(fmt.Sprintf("history=%d", scale), func(b *testing.B) {
			e, err := NewEngine(Config{R: 5})
			mustB(b, err)
			mustB(b, e.RegisterDrug(0, "T", 1, true))
			mustB(b, e.RegisterDrug(0, "H", 1, true))
			addCancelledHistory(b, e, 0, scale, "h")
			now := 1
			rxN := 0
			var pending []string
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				for _, id := range pending { // 清理上一轮，保持状态有界
					mustB(b, e.CancelPrescription(now, id))
				}
				pending = pending[:0]
				mustB(b, e.Inbound(now, "T", k*10))
				for j := 0; j < k; j++ { // K 张预留处方（失效时刻 now+6）
					id := fmt.Sprintf("c%d", rxN)
					rxN++
					mustB(b, e.AcceptPrescription(now, RxInput{ID: id, Patient: "p", IssueTime: now, Lines: []LineInput{{DrugID: "T", Qty: 10}}}))
					pending = append(pending, id)
				}
				for j := 0; j < k; j++ { // K 张欠药处方
					id := fmt.Sprintf("w%d", rxN)
					rxN++
					mustB(b, e.AcceptPrescription(now, RxInput{ID: id, Patient: "p", IssueTime: now, Lines: []LineInput{{DrugID: "T", Qty: 10}}}))
					pending = append(pending, id)
				}
				b.StartTimer()
				now += 6 // 恰为失效时刻：K 个事件 + K 次再分配
				if _, err := e.QueryDrug(now, "T"); err != nil {
					b.Fatalf("QueryDrug: %v", err)
				}
				now++
			}
		})
	}
}
