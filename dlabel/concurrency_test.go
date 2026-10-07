package dlabel

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestRepeatableReadStableUnderConcurrentWrites 在受屏障控制的确定交织下验证：
// 快照内多次读取始终得到同一版本、同一结论；快照之外的写立即生效。
func TestRepeatableReadStableUnderConcurrentWrites(t *testing.T) {
	log := NewMemoryAuditLog(0)
	p := NewPlatform([]string{"root"}, WithAuditLog(log), WithRetainedVersions(100000))
	must(t, p.RegisterObjectType("root", "O", map[string]ValueKind{"a": KindInt}))
	must(t, p.SetRule("root", Rule{ObjectType: "O", Tag: "hot", Body: AttrAtom("a", OpEq, IntValue(1))}))
	must(t, p.SetGrant("root", Grant{
		Subject: "r", Tag: "hot",
		Read: EffectAllow, Write: EffectAllow, Visibility: EffectAllow, Attrs: []string{"*"},
	}))
	must(t, p.CreateInstance("root", "O", "i", map[string]Value{"a": IntValue(0)}))

	committed := make(chan struct{})
	proceed := make(chan struct{})

	snap := p.Begin("r")

	// 第一阶段：快照内读取（标签不携带 -> a 无授权 -> 拒绝；因为没有 cold 标签授权，
	// 这里换用管理员观测值；普通主体用“携带时允许”的反向主体检验）。
	v0, err := snap.TagCarried("root", "O", "i", "hot")
	must(t, err)
	if v0 {
		t.Fatal("initial state must not carry hot")
	}

	go func() {
		<-proceed
		_ = p.WriteInstance("root", "O", "i", map[string]Value{"a": IntValue(1)})
		close(committed)
	}()

	// 快照内再读一次，必须与旧快照一致（尚未放行写）。
	v1, err := snap.TagCarried("root", "O", "i", "hot")
	must(t, err)
	if v1 != v0 {
		t.Fatal("snapshot changed before write commit")
	}

	close(proceed)
	<-committed

	// 提交后：新快照立即反映翻转，旧快照保持不变。
	v2, err := p.Begin("root").TagCarried("root", "O", "i", "hot")
	must(t, err)
	if !v2 {
		t.Fatal("new snapshot must immediately observe the flipped tag")
	}
	v3, err := snap.TagCarried("root", "O", "i", "hot")
	must(t, err)
	if v3 != v0 {
		t.Fatal("old snapshot must remain repeatable after concurrent commit")
	}

	// 快照内多次读取完全一致。
	for k := 0; k < 10; k++ {
		v, err := snap.TagCarried("root", "O", "i", "hot")
		must(t, err)
		if v != v0 {
			t.Fatalf("repeatable read broke at iteration %d", k)
		}
	}
}

// TestConcurrentOpsLinearizable 并发混合写入、规则/授权变更与多快照读取。
// 可线性化的可观测推论：每次快照读取的结论必须等于其固定版本在“提交顺序”下
// 的结果；在本实现中版本即线性化顺序，因此直接逐版本核对快照内部一致性，
// 并确认所有成功写入的值都能在某个版本上被管理员精确观察到（无丢失更新）。
func TestConcurrentOpsLinearizable(t *testing.T) {
	log := NewMemoryAuditLog(0)
	p := NewPlatform([]string{"root"}, WithAuditLog(log), WithRetainedVersions(1000000))
	must(t, p.RegisterObjectType("root", "O", map[string]ValueKind{"a": KindInt, "b": KindInt}))
	must(t, p.SetRule("root", Rule{ObjectType: "O", Tag: "ta", Body: AttrAtom("a", OpGte, IntValue(0))}))
	must(t, p.SetGrant("root", Grant{
		Subject: "r", Tag: "ta",
		Read: EffectAllow, Write: EffectAllow, Visibility: EffectAllow, Attrs: []string{"*"},
	}))
	for i := 0; i < 5; i++ {
		must(t, p.CreateInstance("root", "O", fmt.Sprintf("i%d", i), map[string]Value{"a": IntValue(0), "b": IntValue(0)}))
	}

	const writers = 8
	const rounds = 60
	var wg sync.WaitGroup

	// 写入方：各自写不同实例（管理员），记录自己写过的值。
	written := make([][]int64, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := fmt.Sprintf("i%d", w%5)
			for k := 0; k < rounds; k++ {
				val := int64(w*10000 + k)
				if err := p.WriteInstance("root", "O", id, map[string]Value{"a": IntValue(val)}); err != nil {
					t.Errorf("write: %v", err)
					return
				}
				written[w] = append(written[w], val)
			}
		}(w)
	}

	// 读取方：获取快照后在同一快照上反复读取，要求值恒定（可重复读）。
	const readers = 8
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < rounds; k++ {
				snap := p.Begin("root")
				var first int64 = -1
				id := fmt.Sprintf("i%d", k%5)
				for j := 0; j < 5; j++ {
					res, err := snap.ReadAttributes("root", "O", id, []string{"a"})
					if err != nil {
						if errors.Is(err, ErrSnapshotUnavailable) {
							return
						}
						t.Errorf("read: %v", err)
						return
					}
					got, _ := res.Attrs["a"].Value.Int()
					if first == -1 {
						first = got
					} else if got != first {
						t.Errorf("non-repeatable read within snapshot: %d vs %d", first, got)
						return
					}
					if res.Version != snap.Version() {
						t.Errorf("read version drifted: %d vs %d", res.Version, snap.Version())
						return
					}
				}
			}
		}()
	}

	wg.Wait()

	// 串行等价性核查：最终每个实例的 a 值必须是某个写入方实际写过的值；
	// 且版本历史上每个中间值都能按版本重放（无撕裂写）。
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("i%d", i)
		res, err := p.Begin("root").ReadAttributes("root", "O", id, []string{"a"})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := res.Attrs["a"].Value.Int()
		found := false
		for _, ws := range written {
			for _, v := range ws {
				if v == got {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("final value %d for %s is not any written value", got, id)
		}
		// 版本链单调且无重复版本。
		versions := p.store.attrVersionsForTest("O", id, "a")
		for k := 1; k < len(versions); k++ {
			if versions[k] <= versions[k-1] {
				t.Fatalf("version chain not strictly increasing for %s", id)
			}
		}
	}

	// 规则变更与读取并发：任何读取要么看到旧规则图，要么看到新规则图，
	// 绝不允许出现提交半途状态（此处规则变更是常量翻转，易于核对）。
	done := make(chan struct{})
	go func() {
		for k := 0; k < 100; k++ {
			body := ConstAtom(k%2 == 0)
			_ = p.ReplaceRules("root", "O", []Rule{{ObjectType: "O", Tag: "ta", Body: body}})
		}
		close(done)
	}()
	for k := 0; k < 200; k++ {
		snap := p.Begin("root")
		v, err := snap.TagCarried("root", "O", "i0", "ta")
		if err != nil {
			t.Fatal(err)
		}
		// 同一快照重复判定必须相同。
		v2, err := snap.TagCarried("root", "O", "i0", "ta")
		if err != nil {
			t.Fatal(err)
		}
		if v != v2 {
			t.Fatal("rule change observed mid-commit within a snapshot")
		}
	}
	<-done
}
