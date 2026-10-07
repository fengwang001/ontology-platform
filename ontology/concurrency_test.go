package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// 并发语义：并发调用的最终可观察结果须等价于某个串行执行顺序。
// 本测试在 -race 下运行：写方并发执行授权变更、链接类型增删与
// 覆盖调整，读方并发判定；结束后校验全部授权生效（无丢失更新），
// 且过程中每次判定要么成功、要么返回可分类的无权限。
func TestConcurrentLinearizable(t *testing.T) {
	g := NewGateway(Config{})
	addTypes(g, "A", "B", "C")
	mustLink(t, g, "ab", "A", "B", 3)
	mustLink(t, g, "bc", "B", "C", 3)

	const writers = 8
	actions := make([]Action, writers)
	for i := range actions {
		actions[i] = Action(fmt.Sprintf("perm%d", i))
	}
	known := make(map[Action]bool, writers)
	for _, a := range actions {
		known[a] = true
	}

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				dec, err := g.Decide("alice", "C")
				if err != nil && !errors.Is(err, ErrNoPermission) {
					t.Errorf("unexpected decide error: %v", err)
					return
				}
				// 任一时刻的判定结果都必须对应某个串行前缀：
				// 出现的动作只能是已授予集合的子集。
				for _, a := range dec.Allowed {
					if !known[a] {
						t.Errorf("torn decision: unknown action %q", a)
						return
					}
				}
			}
		}()
	}

	var writersWG sync.WaitGroup
	// 授权变更：每个写方授予一个独立动作。
	for w := 0; w < writers; w++ {
		writersWG.Add(1)
		go func(i int) {
			defer writersWG.Done()
			if err := g.Grant("alice", "A", actions[i]); err != nil {
				t.Errorf("grant: %v", err)
			}
		}(w)
	}
	// 结构调整：各自挂独立的新类型（互不成环），随后调整覆盖并
	// 删除链接。
	for w := 0; w < writers; w++ {
		writersWG.Add(1)
		go func(i int) {
			defer writersWG.Done()
			id := ObjectTypeID(fmt.Sprintf("N%d", i))
			lid := LinkTypeID(fmt.Sprintf("n%d", i))
			g.AddObjectType(id)
			if err := g.AddLinkType(LinkType{
				ID: lid, From: "C", To: id,
				Propagates: true, MaxDepth: 1,
			}); err != nil {
				t.Errorf("add link: %v", err)
			}
			if err := g.SetOverride(id, OverrideReplace); err != nil {
				t.Errorf("set override: %v", err)
			}
			if err := g.RemoveLinkType(lid); err != nil {
				t.Errorf("remove link: %v", err)
			}
		}(w)
	}
	writersWG.Wait()
	close(stop)
	readers.Wait()

	// 终态等价于全部写操作完成后的串行前缀：所有动作生效。
	dec, err := g.Decide("alice", "C")
	mustAllow(t, dec, err, actions...)
	logDecision(t, "alice", "C", dec, err)
}
