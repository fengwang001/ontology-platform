package endpointshard

import (
	"fmt"
	"sync"
	"testing"
)

// 任意并发调用的结果必须等价于某个串行顺序；
// 同一服务上的同步与读取互不交错（查询永远看到完整状态）。
// 配合 go test -race 运行以检测数据竞争。
func TestConcurrentSyncAndQuery(t *testing.T) {
	m := NewManager()
	names := []string{"svc-a", "svc-b", "svc-c"}
	for _, name := range names {
		mustCreate(t, m, name, 3)
	}

	sets := [][]Endpoint{
		{readyEp("a", "cn"), readyEp("b", "us"), ep("c", "cn", true, true)},
		{readyEp("a", "cn"), readyEp("d", "eu"), ep("e", "us", false, false), readyEp("f", "cn")},
		{ep("a", "cn", true, true), ep("g", "eu", true, true)},
		{},
	}

	var writers, readers sync.WaitGroup
	// 写者：并发同步与容量调整。
	for _, name := range names {
		for w := 0; w < 2; w++ {
			writers.Add(1)
			go func(name string, w int) {
				defer writers.Done()
				for i := 0; i < 150; i++ {
					if _, err := m.Sync(name, sets[(i+w)%len(sets)]); err != nil {
						t.Errorf("sync %s: %v", name, err)
						return
					}
					if i%17 == 0 {
						if _, err := m.Resize(name, 2+(i%4)); err != nil {
							t.Errorf("resize %s: %v", name, err)
							return
						}
					}
				}
			}(name, w)
		}
	}
	// 读者：查询结果必须是某个完整状态的视图（不变式与状态无关，可独立校验）。
	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func(r int) {
			defer readers.Done()
			region := []string{"cn", "us", "eu"}[r%3]
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, name := range names {
					res, err := m.Query(name, region)
					if err != nil {
						t.Errorf("query %s: %v", name, err)
						return
					}
					if bad := queryInvariantViolation(res, region); bad != "" {
						t.Errorf("query %s(%s) violated invariant: %s; result=%+v", name, region, bad, res)
						return
					}
					if _, err := m.Describe(name); err != nil {
						t.Errorf("describe %s: %v", name, err)
						return
					}
				}
			}
		}(r)
	}
	// 先等写者全部完成，再停止读者。
	writers.Wait()
	close(stop)
	readers.Wait()

	// 收尾：每个服务同步到固定期望集，与朴素模型对照最终状态。
	model := newNaiveModel()
	for i, name := range names {
		if err := m.DeleteService(name); err != nil {
			t.Fatalf("delete %s: %v", name, err)
		}
		mustCreate(t, m, name, 2+i)
		model.create(name, 2+i)
		desired := sets[i%len(sets)]
		repGot := mustSync(t, m, name, desired)
		repWant := model.sync(name, desired)
		if !reportEqual(repGot, repWant) {
			t.Fatalf("final report mismatch on %s\ngot %+v\nwant %+v", name, repGot, repWant)
		}
		if got, want := mustDescribe(t, m, name), model.describe(name); !serviceViewEqual(got, want) {
			t.Fatalf("final view mismatch on %s\ngot %+v\nwant %+v", name, got, want)
		}
	}
	t.Logf("输入: 并发同步/调整/查询; 实际输出: 无竞争、查询不变式成立; 判定依据: 最终状态与朴素模型一致")
}

// queryInvariantViolation 校验查询结果的结构不变式，返回违例描述或空串。
func queryInvariantViolation(res QueryResult, region string) string {
	seenOther := false
	lastSame, lastOther := "", ""
	for _, ep := range res.Endpoints {
		if res.Fallback {
			if !ep.Serveable() || !ep.IsTerminating() {
				return fmt.Sprintf("fallback result contains non-serveable-terminating endpoint %+v", ep)
			}
		} else if !ep.Ready() {
			return fmt.Sprintf("primary result contains non-ready endpoint %+v", ep)
		}
		if ep.Region == region {
			if seenOther {
				return fmt.Sprintf("same-region endpoint %s appears after other-region endpoint", ep.ID)
			}
			if ep.ID < lastSame {
				return fmt.Sprintf("same-region group not sorted: %s < %s", ep.ID, lastSame)
			}
			lastSame = ep.ID
		} else {
			seenOther = true
			if ep.ID < lastOther {
				return fmt.Sprintf("other-region group not sorted: %s < %s", ep.ID, lastOther)
			}
			lastOther = ep.ID
		}
	}
	return ""
}

// 并发创建/删除/同步同一服务名：错误类别只能是允许集合内的值。
func TestConcurrentCreateDelete(t *testing.T) {
	m := NewManager()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				switch g % 3 {
				case 0:
					err := m.CreateService("svc", 2)
					if err != nil {
						if k, _ := KindOf(err); k != KindServiceAlreadyExists {
							t.Errorf("create: unexpected kind %v", k)
							return
						}
					}
				case 1:
					err := m.DeleteService("svc")
					if err != nil {
						if k, _ := KindOf(err); k != KindServiceNotFound {
							t.Errorf("delete: unexpected kind %v", k)
							return
						}
					}
				case 2:
					_, err := m.Sync("svc", readyEps("a", "b"))
					if err != nil {
						if k, _ := KindOf(err); k != KindServiceNotFound {
							t.Errorf("sync: unexpected kind %v", k)
							return
						}
					}
				}
			}
		}(g)
	}
	wg.Wait()
	t.Logf("输入: 并发创建/删除/同步同名服务; 实际输出: 错误类别均在允许集合内; 判定依据: 无恐慌、无意外类别")
}
