package temporalauth

import (
	"testing"
)

func buildComplexityStore(n int) (*Service, error) {
	catalog := NewZoneCatalog(
		&ZoneRules{ID: "A", BaseOffset: 0},
		&ZoneRules{ID: "B", BaseOffset: 3600},
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
		return nil, err
	}
	// 追加 n 个历史地区时区版本，全部在判定时刻之前很久生效。
	vf := Instant(1)
	for i := 0; i < n; i++ {
		z := "A"
		if i%2 == 1 {
			z = "B"
		}
		if err := store.AppendRegionVersion("R", RegionVersion{ValidFrom: vf, ZoneID: z}); err != nil {
			return nil, err
		}
		vf += 10
	}
	store.RegisterObject("obj", "ot", "R")
	if err := store.AppendWindowVersion("ot", "p", WindowVersion{
		ValidFrom: 0,
		Rule:      WindowRule{RegionID: "R", Start: Civil{2100, 1, 1, 0, 0, 0}, End: Civil{2100, 1, 1, 0, 0, 1}},
	}); err != nil {
		return nil, err
	}
	svc.SetCrossCheck(false) // 复杂度度量只针对生产判定路径（朴素对照本身刻意 O(n)）
	svc.SetAudit(false)
	return svc, nil
}

// TestRegionResolutionLogarithmic 通过探针直接验证：生效版本解析的比较次数
// 上界为 ceil(log2(n+1))+1，而不是随版本数 n 线性增长。该探针可独立复核。
func TestRegionResolutionLogarithmic(t *testing.T) {
	sizes := []int{16, 256, 4096}
	var prevProbes, prevN int
	for _, n := range sizes {
		svc, err := buildComplexityStore(n)
		if err != nil {
			t.Fatal(err)
		}
		svc.store.ResetProbes()
		svc.View(ViewRequest{ObjectID: "obj", ObjectType: "ot", Property: "p",
			Viewer: &Viewer{ID: "v"}, Now: vf(n)})
		probes := int(svc.store.RegionProbes())
		bound := 0
		for x := n + 1; x > 0; x >>= 1 {
			bound++
		}
		bound++ // +1 常数项
		if probes > bound {
			t.Fatalf("n=%d probes=%d exceeds O(log n) bound=%d", n, probes, bound)
		}
		if prevN > 0 {
			// n 扩大 16 倍，探针增长不得超过线性比例，实际应近似 +4。
			growth := probes - prevProbes
			if growth > bound {
				t.Fatalf("probe growth too large: %d (n %d->%d)", growth, prevN, n)
			}
		}
		prevProbes, prevN = probes, n
	}
}

func vf(n int) Instant {
	// 判定时刻晚于所有追加版本（最大 ValidFrom = 1 + 10*(n-1)）。
	return Instant(10*n + 100)
}

func BenchmarkViewVsRegionVersions(b *testing.B) {
	for _, n := range []int{100, 10000} {
		svc, err := buildComplexityStore(n)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(sizeName(n), func(b *testing.B) {
			req := ViewRequest{ObjectID: "obj", ObjectType: "ot", Property: "p",
				Viewer: &Viewer{ID: "v"}, Now: vf(n)}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				svc.View(req)
			}
		})
	}
}

func sizeName(n int) string {
	switch n {
	case 100:
		return "versions_100"
	case 10000:
		return "versions_10000"
	}
	return "versions"
}
