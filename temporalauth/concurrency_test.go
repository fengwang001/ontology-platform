package temporalauth

import (
	"sync"
	"testing"
)

// TestConcurrentSerializability 并发混合查看、地区时区版本追加与窗口规则
// 追加。配合 `go test -race` 验证无数据竞争；并验证每次查看都能观察到某个
// 一致序列号，且审计结论与朴素模型一致（等价于某种全局串行顺序）。
func TestConcurrentSerializability(t *testing.T) {
	catalog := NewZoneCatalog(
		&ZoneRules{ID: "A", BaseOffset: 0},
		&ZoneRules{ID: "B", BaseOffset: 1800},
		&ZoneRules{ID: "C", BaseOffset: 3600},
	)
	store := NewStore(catalog)
	audit := NewAuditLog()
	svc := NewService(store, audit)

	store.AddObjectType(&ObjectType{ID: "ot", Versions: []ObjectTypeVersion{
		{ValidFrom: 0, Properties: map[string]PropertySpec{
			"p": {Name: "p", Kind: KindTemporal},
		}},
	}})
	if err := store.AppendRegionVersion("R", RegionVersion{ValidFrom: 0, ZoneID: "A"}); err != nil {
		t.Fatal(err)
	}
	store.RegisterObject("obj", "ot", "R")
	if err := store.AppendWindowVersion("ot", "p", WindowVersion{
		ValidFrom: 0,
		Rule:      WindowRule{RegionID: "R", Start: Civil{1970, 1, 1, 0, 0, 0}, End: Civil{2100, 1, 1, 0, 0, 0}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutTemporalRecord(TemporalRecord{
		ObjectID: "obj", Property: "p", RecordedAt: 0, RecordZone: "A",
		Wall: Civil{1970, 1, 1, 0, 0, 0},
	}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 并发追加地区时区版本（仅向后生效）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		zones := []string{"A", "B", "C"}
		vf := Instant(1_000_000)
		for i := 0; i < 100; i++ {
			_ = store.AppendRegionVersion("R", RegionVersion{ValidFrom: vf, ZoneID: zones[i%3]})
			vf += 1000
		}
	}()

	// 并发追加窗口版本。
	wg.Add(1)
	go func() {
		defer wg.Done()
		vf := Instant(2_000_000)
		for i := 0; i < 100; i++ {
			_ = store.AppendWindowVersion("ot", "p", WindowVersion{
				ValidFrom: vf,
				Rule: WindowRule{
					RegionID: "R",
					Start:    Civil{1970, 1, 1, 0, 0, 0},
					End:      Civil{2100, 1, 1, 0, 0, 0},
				},
			})
			vf += 1000
		}
	}()

	// 并发查看。
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					svc.View(ViewRequest{ObjectID: "obj", ObjectType: "ot", Property: "p",
						Viewer: &Viewer{ID: "v"}, Now: 10})
				}
			}
		}()
	}

	// 让写入者完成后再停止读取者。
	done := make(chan struct{})
	go func() {
		// 仅等待前两个写 goroutine：此处简化为等待固定数量 append 落地。
		for store.Seq() < 202 {
		}
		close(done)
	}()
	<-done
	close(stop)
	wg.Wait()

	for _, e := range audit.Entries() {
		if e.CrossCheck && !e.CrossMatch {
			t.Fatalf("serializability cross-check mismatch: %+v", e)
		}
	}
}
