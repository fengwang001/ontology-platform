package ontology

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"runtime"
	"sync"
	"testing"
)

// concurrentEnv 构造一个对拍环境：
//   - 多个 Person / Org 实例；
//   - 两个同时作用于同一对对象类型的链接类型（容量各不相同），
//     用来验证跨链接类型的基数/重复独立性。
type concurrentEnv struct {
	store *Store
	naive *NaiveStore

	persons []string
	orgs    []string
	lts     []string
	roles   []string
}

func newConcurrentEnv() *concurrentEnv {
	store := NewStore()
	naive := NewNaiveStore()

	env := &concurrentEnv{store: store, naive: naive}

	// 对象类型与链接类型直接登记。
	if err := store.RegisterObjectType(NewObjectType("Person", "人")); err != nil {
		panic(err)
	}
	if err := store.RegisterObjectType(NewObjectType("Org", "组织")); err != nil {
		panic(err)
	}
	naive.RegisterObjectType(NewObjectType("Person", "人"))
	naive.RegisterObjectType(NewObjectType("Org", "组织"))

	linkSpecs := []struct {
		id        string
		fwd, back Cardinality
		discrim   []string
	}{
		{"employs", AtMost(2), AtMost(1), []string{"role"}},
		{"audits", AtMost(1), Unlimited(), []string{"role", "scope"}},
	}
	for _, spec := range linkSpecs {
		lt := NewLinkType(spec.id, "Person", "Org", spec.fwd, spec.back, spec.discrim)
		if err := store.RegisterLinkType(lt); err != nil {
			panic(err)
		}
		naive.RegisterLinkType(NewLinkType(spec.id, "Person", "Org", spec.fwd, spec.back, spec.discrim))
		env.lts = append(env.lts, spec.id)
	}

	ctx := context.Background()
	for i := 0; i < 4; i++ {
		p := fmt.Sprintf("p%d", i)
		o := fmt.Sprintf("o%d", i)
		if _, err := store.CreateObject(ctx, p, "Person"); err != nil {
			panic(err)
		}
		if _, err := store.CreateObject(ctx, o, "Org"); err != nil {
			panic(err)
		}
		naive.CreateObject(p, "Person")
		naive.CreateObject(o, "Org")
		env.persons = append(env.persons, p)
		env.orgs = append(env.orgs, o)
	}
	env.roles = []string{"dev", "pm", "qa", "intern"}
	return env
}

// TestConcurrentCreateDeleteVsNaive 大量并发创建/删除随机交织后：
//  1. 最终在库链接集合（按内容多重集）与按审计全序在朴素模型上
//     逐一串行回放的结果一致；
//  2. 每个 (链接类型,方向,尾实例) 的基数计数一致；
//  3. 每个请求的实时返回码与它在审计序位置上朴素模型的返回码一致。
func TestConcurrentCreateDeleteVsNaive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	for iter := 0; iter < 30; iter++ {
		rng := rand.New(rand.NewSource(int64(1700) + int64(iter)))
		env := newConcurrentEnv()

		const totalOps = 400
		// 预先生成一组确定的随机操作（含跨链接类型、双方向、
		// 同组合创建/删除/重建、对象逻辑删除）。
		type op struct {
			create *CreateLinkInput
			delObj bool
			delTgt string
		}
		ops := make([]op, totalOps)
		var wg sync.WaitGroup
		start := make(chan struct{})
		ctx := context.Background()

		for i := 0; i < totalOps; i++ {
			roll := rng.Intn(100)
			switch {
			case roll < 65: // 创建
				ltID := env.lts[rng.Intn(len(env.lts))]
				dir := Forward
				var tail, head string
				if rng.Intn(2) == 0 {
					tail = env.persons[rng.Intn(len(env.persons))]
					head = env.orgs[rng.Intn(len(env.orgs))]
				} else {
					dir = Backward
					tail = env.orgs[rng.Intn(len(env.orgs))]
					head = env.persons[rng.Intn(len(env.persons))]
				}
				disc := map[string]string{"role": env.roles[rng.Intn(len(env.roles))]}
				if ltID == "audits" {
					disc["scope"] = []string{"team", "company"}[rng.Intn(2)]
				}
				in := &CreateLinkInput{
					LinkTypeID: ltID, Direction: dir, TailID: tail, HeadID: head,
					Discriminator: disc,
				}
				ops[i] = op{create: in}
			case roll < 97: // 删除：运行时从在库集合中随机挑一个，审计记录里带完整身份
				ops[i] = op{}
			default: // 逻辑删除一个对象
				ops[i] = op{delObj: true, delTgt: env.persons[rng.Intn(len(env.persons))]}
			}
		}

		var liveMu sync.Mutex
		liveIDs := map[string]struct{}{}

		workers := runtime.GOMAXPROCS(0) * 2
		if workers < 4 {
			workers = 4
		}
		ch := make(chan int)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for i := range ch {
					switch {
					case ops[i].create != nil:
						l, err := env.store.CreateLink(ctx, *ops[i].create)
						if err == nil && l != nil {
							liveMu.Lock()
							liveIDs[l.ID()] = struct{}{}
							liveMu.Unlock()
						}
					case ops[i].create == nil && !ops[i].delObj:
						liveMu.Lock()
						var victim string
						for id := range liveIDs {
							victim = id
							break
						}
						if victim != "" {
							delete(liveIDs, victim)
						}
						liveMu.Unlock()
						if victim != "" {
							err := env.store.DeleteLink(ctx, victim)
							if err == nil {
							} else {
								liveMu.Lock()
								liveIDs[victim] = struct{}{}
								liveMu.Unlock()
							}
						}
					case ops[i].delObj:
						_ = env.store.DeleteObject(ctx, ops[i].delTgt)
					}
				}
			}()
		}
		close(start)
		for i := 0; i < totalOps; i++ {
			ch <- i
		}
		close(ch)
		wg.Wait()

		// 以审计记录给出的全序在朴素模型上串行回放，逐笔核对返回码，
		// 并维护 store 链接 ID -> 朴素 ID 的映射（删除按映射转发）。
		naive := NewNaiveStoreFrom(env.naive) // 复用同一套静态声明
		idMap := map[string]int{}
		log := env.store.AuditLog()
		// 审计只包含真正进入仲裁的记录；delete 空转操作无记录，跳过。
		for _, rec := range log {
			switch rec.Operation {
			case "create_link":
				nid, code := naive.CreateLink(rec.CreateInput)
				if code != rec.Result {
					t.Fatalf("iter %d seq %d: code mismatch naive=%s store=%s input=%s",
						iter, rec.Seq, code, rec.Result, rec.Input)
				}
				if code == CodeAccepted {
					idMap[rec.LinkID] = nid
				}
			case "delete_link":
				storeID := rec.TargetID
				nid, ok := idMap[storeID]
				if !ok {
					t.Fatalf("iter %d: deleted store link %q has no naive mapping", iter, storeID)
				}
				if !naive.DeleteLink(nid) {
					t.Fatalf("iter %d: naive delete of %d failed (store seq %d)", iter, nid, rec.Seq)
				}
				if rec.Result != CodeAccepted {
					t.Fatalf("iter %d: recorded delete result %s", iter, rec.Result)
				}
				delete(idMap, storeID)
			case "delete_object":
				if rec.Result == CodeAccepted {
					if !naive.DeleteObject(rec.TargetID) {
						t.Fatalf("iter %d: naive delete-object %s failed", iter, rec.TargetID)
					}
				}
			}
		}

		// 最终集合与计数对拍。
		for _, ltID := range env.lts {
			gotSet := storeActiveSignature(env.store, ltID)
			wantSet := naive.ActiveSignature(ltID)
			if len(gotSet) == 0 {
				gotSet = []string{}
			}
			if len(wantSet) == 0 {
				wantSet = []string{}
			}
			if !reflect.DeepEqual(gotSet, wantSet) {
				t.Fatalf("iter %d lt %s active set mismatch:\n store=%v\n naive=%v", iter, ltID, gotSet, wantSet)
			}
			for _, p := range env.persons {
				compareCount(t, env.store, naive, ltID, Forward, p, iter)
			}
			for _, o := range env.orgs {
				compareCount(t, env.store, naive, ltID, Backward, o, iter)
			}
		}
	}
}

func compareCount(t *testing.T, s *Store, n *NaiveStore, ltID string, d Direction, tail string, iter int) {
	t.Helper()
	got, err := s.CountLinks(context.Background(), ltID, tail, d)
	if err != nil {
		t.Fatal(err)
	}
	want := n.Count(ltID, d, tail)
	if got != want {
		t.Fatalf("iter %d count (%s,%s,%s): store=%d naive=%d", iter, ltID, d, tail, got, want)
	}
}

func storeActiveSignature(s *Store, ltID string) []string {
	active := s.ActiveLinks(ltID)
	out := make([]string, 0, len(active))
	for _, l := range active {
		out = append(out, l.linkTypeID+"|"+l.direction.String()+"|"+l.tailID+"|"+l.headID+"|"+
			canonicalDiscriminator(s.linkTypes[ltID], l.discrim))
	}
	sortStrings(out)
	return out
}

// NewNaiveStoreFrom 复制静态声明（类型/链接类型/对象），但不带任何在库链接，
// 供从空操作序列开始回放。
func NewNaiveStoreFrom(src *NaiveStore) *NaiveStore {
	n := NewNaiveStore()
	for k := range src.types {
		n.types[k] = struct{}{}
	}
	for k, v := range src.links {
		n.links[k] = v
	}
	for k, v := range src.objects {
		n.objects[k] = v
	}
	return n
}
