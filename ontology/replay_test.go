package ontology

import (
	"errors"
	"fmt"
	"testing"
)

// 确定性交错下，把真实实现的“每次尝试点”重建为一张尝试级调度表，
// 交给带重试预算的朴素串行模型重放，断言两类实现的
// 逐尝试原因（冲突/基数/提交）与最终分类（含耗尽）完全一致，
// 且真实实现每次尝试记录的版本就是重放点版本——可重放核验。
func TestDeterministicInterleavingReplay(t *testing.T) {
	const maxAttempts = 3

	// 场景：受害者 v 与不断插入的竞争者 c1,c2,... 争抢。
	// 脚本：v 的前 maxAttempts 次尝试点，每次都有一个竞争者
	// 在“新鲜读取之后、锁内复核之前”抢先提交，最终 v 耗尽。
	store := NewStore()
	store.AddObject("o")
	store.AddCardinality("o", Cardinality{LinkType: testLink, Direction: Outgoing, Max: 1000})

	competitor := 0

	hooked := NewSubmitter(store, RetryPolicy{MaxAttempts: maxAttempts}).
		WithHook(func(change Change, attempt int, read *Snapshot) {
			competitor++
			cid := competitor
			ch := Change{ObjectID: "o", BaseVersion: read.Version, Ops: []LinkOp{
				{LinkType: "c", Direction: Outgoing, OtherID: fmt.Sprintf("c%d", cid), Add: true},
			}}
			if _, err := NewSubmitter(store, RetryPolicy{MaxAttempts: 1}).Submit(ch); err != nil {
				t.Fatalf("competitor %d must commit: %v", cid, err)
			}
		})

	victimChange := addChange("v")
	res, err := hooked.Submit(victimChange)
	if !errors.Is(err, ErrRetriesExhausted) {
		t.Fatalf("want exhaustion, got %v", err)
	}

	// 重建尝试级调度表：按真实版本号排序（竞争者提交点与受害者尝试点
	// 都锚定在具体版本上），竞争者在前、受害者在对应版本冲突点在后。
	// 直接按脚本生成顺序即可（每次 hook 追加竞争者，循环后追加受害者）：
	// 真实受害者初始基线为 0；第 1 次尝试的窗口内竞争者 1 已提交，
	// 受害者锁内读到版本 1 → 冲突。因此调度表以竞争者 1 开头，
	// 之后每轮是“竞争者提交 → 受害者在新版本冲突”。
	schedule := []int{1001, 0}
	peers := map[int]string{}
	peers[1001] = "c1"
	for i := 2; i <= competitor; i++ {
		schedule = append(schedule, 1000+i, 0) // 竞争者成功，随后受害者冲突
		peers[1000+i] = fmt.Sprintf("c%d", i)
	}
	peers[0] = "v"

	model := newNaiveModel(1000)
	model.runRetry(schedule, peers, maxAttempts, map[int]int64{0: 0})

	// 朴素模型对受害者（req=0）的逐尝试决策：每次都是 conflict，
	// 最后追加 exhausted。
	victimKinds := []string{}
	for _, d := range model.decisions {
		if d.req == 0 {
			victimKinds = append(victimKinds, d.kind)
		}
	}
	wantKinds := []string{"conflict", "conflict", "conflict", "exhausted"}
	if len(victimKinds) != len(wantKinds) {
		t.Fatalf("model victim kinds %v want %v", victimKinds, wantKinds)
	}
	for i := range wantKinds {
		if victimKinds[i] != wantKinds[i] {
			t.Fatalf("model victim kind[%d]=%s want %s", i, victimKinds[i], wantKinds[i])
		}
	}

	// 真实轨迹与模型逐点版本对齐核验：受害者第 k 次尝试读到的版本
	// 必须等于模型中第 k 个竞争者提交后的版本（即可由调度表精确重放）。
	if len(res.Attempts) != maxAttempts {
		t.Fatalf("attempts %d", len(res.Attempts))
	}
	for i, d := range res.Attempts {
		wantVersion := int64(i + 1) // 竞争者 1..i 各推进一次
		if d.Read.Version != wantVersion {
			t.Fatalf("attempt %d read version %d want replay version %d",
				i+1, d.Read.Version, wantVersion)
		}
		if d.Reason != ReasonVersionConflict {
			t.Fatalf("attempt %d reason %v want conflict", i+1, d.Reason)
		}
	}

	// 受害者从未写入；最终可见集合恰好是竞争者集合。
	final := store.Snapshot("o")
	if final.Links[keyOf(testLink, Outgoing)] != nil &&
		final.Links[keyOf(testLink, Outgoing)]["v"] {
		t.Fatal("exhausted victim must never appear in final links")
	}
	if final.Version != int64(competitor) {
		t.Fatalf("version %d want exactly %d competitor commits", final.Version, competitor)
	}
}
