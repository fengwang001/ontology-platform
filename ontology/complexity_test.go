package ontology

import (
	"context"
	"reflect"
	"testing"
)

// churnBuild 构造“历史链接量巨大、当前有效链接很少”的场景：
// 在 p->o 之间反复创建并撤销共 history 条链接（用序号区分），
// 最终只保留 keep 条有效链接。
func churnBuild(fail func(string, ...any), history, keep int) *Store {
	s := NewStore()
	if err := s.RegisterObjectType(NewObjectType("Person", "人")); err != nil {
		fail("register Person: %v", err)
	}
	if err := s.RegisterObjectType(NewObjectType("Org", "组织")); err != nil {
		fail("register Org: %v", err)
	}
	lt := NewLinkType("employs", "Person", "Org", AtMost(uint64(keep+1)), Unlimited(), []string{"seq"})
	if err := s.RegisterLinkType(lt); err != nil {
		fail("register link type: %v", err)
	}
	ctx := context.Background()
	if _, err := s.CreateObject(ctx, "p", "Person"); err != nil {
		fail("create p: %v", err)
	}
	if _, err := s.CreateObject(ctx, "o", "Org"); err != nil {
		fail("create o: %v", err)
	}
	var kept []string
	for i := 0; i < history; i++ {
		l, err := s.CreateLink(ctx, CreateLinkInput{
			LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
			Discriminator: map[string]string{"seq": itoa(i)},
		})
		if err != nil {
			fail("create %d: %v", i, err)
		}
		kept = append(kept, l.ID())
		if len(kept) > keep {
			victim := kept[0]
			kept = kept[1:]
			if err := s.DeleteLink(ctx, victim); err != nil {
				fail("delete %s: %v", victim, err)
			}
		}
	}
	return s
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

type internalSnapshot struct {
	links  int
	byID   int
	counts map[countKey]int
}

func snapshotInternals(s *Store) internalSnapshot {
	c := make(map[countKey]int, len(s.counts))
	for k, v := range s.counts {
		c[k] = v
	}
	return internalSnapshot{links: len(s.links), byID: len(s.byID), counts: c}
}

// TestCountIndependentOfHistory 给出可验证的结构性证明：
// 历史上创建/撤销过成千上万条链接后，
//  1. 活跃链接索引与 ID 索引只包含当前 keep 条，无任何历史残留；
//  2. 计数桶总数只与“当前仍有出链的尾实例数”相关（归零即删桶）；
//  3. 计数只是对 counts 哈希表做一次 O(1) 查表，读取的结构中
//     根本不含已撤销链接，因此成本不可能随历史总量增长；
//  4. 审计日志可以任意长，但计数路径完全不触碰审计。
func TestCountIndependentOfHistory(t *testing.T) {
	const history = 2000
	const keep = 3
	s := churnBuild(func(format string, args ...any) { t.Fatalf(format, args...) }, history, keep)

	if len(s.links) != keep {
		t.Fatalf("active index size=%d, want %d (must not retain revoked links)", len(s.links), keep)
	}
	if len(s.byID) != keep {
		t.Fatalf("id index size=%d, want %d", len(s.byID), keep)
	}
	ck := countKey{linkTypeID: "employs", direction: Forward, tailID: "p"}
	if got := s.counts[ck]; got != keep {
		t.Fatalf("stored counter=%d want %d", got, keep)
	}
	if len(s.counts) != 1 {
		t.Fatalf("count buckets=%d, want 1 (zero buckets are deleted)", len(s.counts))
	}

	before := snapshotInternals(s)
	_ = s.AuditLog()
	if after := snapshotInternals(s); !reflect.DeepEqual(before, after) {
		t.Fatal("reading audit log mutated internal structures")
	}

	got, err := s.CountLinks(context.Background(), "employs", "p", Forward)
	if err != nil || got != keep {
		t.Fatalf("CountLinks=%d err=%v, want %d", got, err, keep)
	}

	// 朴素模型在同样历史后给出相同计数（语义对拍，非性能断言）。
	n := NewNaiveStore()
	n.RegisterObjectType(NewObjectType("Person", "人"))
	n.RegisterObjectType(NewObjectType("Org", "组织"))
	n.RegisterLinkType(NewLinkType("employs", "Person", "Org", AtMost(keep+1), Unlimited(), []string{"seq"}))
	n.CreateObject("p", "Person")
	n.CreateObject("o", "Org")
	var keptIDs []int
	for i := 0; i < history; i++ {
		id, code := n.CreateLink(CreateLinkInput{
			LinkTypeID: "employs", Direction: Forward, TailID: "p", HeadID: "o",
			Discriminator: map[string]string{"seq": itoa(i)},
		})
		if code != CodeAccepted {
			t.Fatalf("naive create %d -> %s", i, code)
		}
		keptIDs = append(keptIDs, id)
		if len(keptIDs) > keep {
			victim := keptIDs[0]
			keptIDs = keptIDs[1:]
			if !n.DeleteLink(victim) {
				t.Fatalf("naive delete %d", victim)
			}
		}
	}
	if n.Count("employs", Forward, "p") != keep {
		t.Fatal("naive reference count diverges")
	}
}

// BenchmarkCountSmallHistory / BenchmarkCountLargeHistory 供人工核验：
// 历史量扩大 100 倍、当前有效量不变时，单次 CountLinks 耗时应基本持平。
//
//	go test -bench=BenchmarkCount -benchmem ./ontology/
func BenchmarkCountSmallHistory(b *testing.B) { benchmarkCount(b, 200) }
func BenchmarkCountLargeHistory(b *testing.B) { benchmarkCount(b, 20000) }

func benchmarkCount(b *testing.B, history int) {
	s := churnBuild(func(format string, args ...any) { b.Fatalf(format, args...) }, history, 3)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := s.CountLinks(ctx, "employs", "p", Forward)
		if err != nil || got != 3 {
			b.Fatalf("count=%d err=%v", got, err)
		}
	}
}
