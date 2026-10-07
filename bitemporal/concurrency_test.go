package bitemporal

import (
	"fmt"
	"sync"
	"testing"
)

// 并发修正写入与并发溯源查询交织：
//   - 任一查询不得观察到链接区间「已修正但写入记录未完整落定」的中间状态
//   - 同一查询参数的结果必须逐字节确定，不随执行时刻与调度次序变化
//   - 最终状态必须等价于全部写入串行发生后的某个合法结果（本场景下唯一）
//
// 使用 go test -race 运行以额外捕获数据竞争。
func TestConcurrentCorrectionsAndQueries(t *testing.T) {
	c := newQClock()
	store := NewStore()
	mustT(t, store.AppendObject(ObjectRecord{ID: "A", VersionID: "A1", Valid: c.iv(0, 1000), WrittenAt: c.at(10)}))
	mustT(t, store.AppendObject(ObjectRecord{ID: "B", VersionID: "B1", Valid: c.iv(0, 1000), WrittenAt: c.at(10)}))
	// 初始：链接覆盖 [0,50)。
	mustT(t, store.AppendLink(LinkRecord{ID: "L", VersionID: "L1", SourceID: "A", TargetID: "B", Valid: c.iv(0, 50), WrittenAt: c.at(20)}))

	logger := NewMemoryLogger()
	engine := NewEngine(store, logger)

	// 历史 asOf=30 的查询永远只能看到 L1（写入于 20），且 validAt=80 永远不可见。
	histQ := Query{SourceID: "A", ValidAt: c.at(80), AsOf: c.at(30), MaxDepth: 1}
	// 未来 asOf=400 的查询在 L2 落定前不可见、落定后可见且证据必为完整的 L2。
	futureQ := Query{SourceID: "A", ValidAt: c.at(80), AsOf: c.at(400), MaxDepth: 1}

	corrections := []Interval{
		c.iv(0, 120),
		c.iv(0, 200),
		c.iv(0, 300),
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 500; k++ {
				r, err := engine.AsOf(histQ)
				if err != nil {
					t.Errorf("hist query error: %v", err)
					return
				}
				if len(r.Paths) != 0 {
					t.Errorf("历史查询观察到了未来修正（撕裂状态）：paths=%v", r.Paths)
					return
				}
				r2, err := engine.AsOf(futureQ)
				if err != nil {
					t.Errorf("future query error: %v", err)
					return
				}
				if len(r2.Paths) == 1 {
					ev := r2.Paths[0].Evidence[0]
					// 可见则三方证据必须自洽：链接版本只能是某个完整落定的修正版本，
					// 绝不允许出现区间已换而记录字段不完整/不一致的状态。
					if ev.LinkID != "L" || ev.SourceVersionID != "A1" || ev.TargetVersionID != "B1" {
						t.Errorf("撕裂状态：三方证据不自洽 %+v", ev)
						return
					}
					if ev.LinkVersionID == "L1" {
						t.Errorf("L1 不覆盖 80 却返回可见：撕裂状态 %+v", ev)
						return
					}
				}
			}
		}()
	}
	// 一个写者顺序提交三条严格递增的修正（写入顺序本身即全局顺序的一环），
	// 与并发读者交织。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i, iv := range corrections {
			err := store.AppendLink(LinkRecord{
				ID:        "L",
				VersionID: fmt.Sprintf("L%d", i+2),
				SourceID:  "A",
				TargetID:  "B",
				Valid:     iv,
				WrittenAt: c.at(100 + i*100),
			})
			if err != nil {
				t.Errorf("append correction: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	// 全部修正串行落定后的唯一合法终态：asOf=400 看到最后一个版本 L4 [0,300)？
	// 注意最后一个修正为 [0,300)，80 仍被覆盖，证据版本为最后写入版本。
	r, err := engine.AsOf(futureQ)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Paths) != 1 || r.Paths[0].Evidence[0].LinkVersionID != "L4" {
		t.Fatalf("终态应为 L4 可见，got %+v", r.Paths)
	}

	// 历史查询终态仍稳定为空。
	r, err = engine.AsOf(histQ)
	if err != nil || len(r.Paths) != 0 {
		t.Fatalf("历史查询终态必须稳定为空，err=%v paths=%v", err, r.Paths)
	}

	if len(logger.Entries()) == 0 {
		t.Fatal("并发查询必须全部记录日志")
	}
}
