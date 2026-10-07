package router

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/ontology/migration"
)

func newMigratedService(t *testing.T) *Service {
	t.Helper()
	svc := NewService(migration.NewDeclaration())
	err := svc.AmendDeclaration([]migration.Mapping{
		{Property: "name", Action: migration.ActionRetain},
		{Property: "nick", Action: migration.ActionDeprecate},
		{Property: "age", Action: migration.ActionAddDefault, Default: 18},
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func mustRead(t *testing.T, svc *Service, id string, v Version) map[string]any {
	t.Helper()
	got, err := svc.Read(id, v)
	if err != nil {
		t.Fatalf("read %s(%s): %v", id, v, err)
	}
	return got
}

// 新版本读取未回填实例必须即时现算视图,且与回填完成后读到的结果完全一致。
func TestNewVersionReadComputesViewOnTheFly(t *testing.T) {
	svc := newMigratedService(t)
	if err := svc.Create("a", VersionOld, map[string]any{"name": "ada", "nick": "a"}); err != nil {
		t.Fatal(err)
	}
	before := mustRead(t, svc, "a", VersionNew)
	t.Logf("输入: 未回填实例 a 的新版本读; 实际输出: %v; 依据: 按迁移声明现算, nick 废弃、age 取默认值", before)
	want := map[string]any{"name": "ada", "age": 18}
	if !reflect.DeepEqual(before, want) {
		t.Fatalf("on-the-fly view = %v, want %v", before, want)
	}
	// 读取不改变回填状态。
	if _, migrated := svc.Status("a"); migrated {
		t.Fatal("read must not change backfill state")
	}
	// 回填后新版本读到的结果必须与现算视图完全一致。
	task := svc.PrepareBackfill("a")
	if outcome := task.Apply(); outcome != BackfillApplied {
		t.Fatalf("backfill outcome = %v", outcome)
	}
	after := mustRead(t, svc, "a", VersionNew)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("new-version read before backfill %v != after backfill %v", before, after)
	}
	t.Logf("依据: 回填前后新版本读结果一致 (%v), 等价于迁移已完成", after)
}

// 旧版本写入未回填实例必须等价转换为新版本写入:应用默认值、丢弃废弃属性,
// 且写入完成后实例视为已回填。
func TestOldVersionWriteIsConvertedToNewVersionWrite(t *testing.T) {
	svc := newMigratedService(t)
	if err := svc.Create("a", VersionOld, map[string]any{"name": "ada", "nick": "a"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Write("a", VersionOld, map[string]any{"name": "ada2"}); err != nil {
		t.Fatal(err)
	}
	exists, migrated := svc.Status("a")
	if !exists || !migrated {
		t.Fatalf("old-version write must backfill the instance, status=(%v,%v)", exists, migrated)
	}
	got := mustRead(t, svc, "a", VersionNew)
	want := map[string]any{"name": "ada2", "age": 18}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("converted write = %v, want %v (defaults applied, deprecated dropped)", got, want)
	}
	t.Logf("输入: 旧版本写 name=ada2; 实际输出(新版本读): %v; 依据: 旧写入按声明转换, 应用 age 默认值并丢弃 nick", got)
}

// 同一数据分别经旧版本写与新版本写,最终新版本视图必须一致。
func TestOldAndNewWritesConverge(t *testing.T) {
	svc := newMigratedService(t)
	for _, id := range []string{"a", "b"} {
		if err := svc.Create(id, VersionOld, map[string]any{"name": "x", "nick": "n"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Write("a", VersionOld, map[string]any{"name": "y"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Write("b", VersionNew, map[string]any{"name": "y"}); err != nil {
		t.Fatal(err)
	}
	va, vb := mustRead(t, svc, "a", VersionNew), mustRead(t, svc, "b", VersionNew)
	if !reflect.DeepEqual(va, vb) {
		t.Fatalf("old write view %v != new write view %v", va, vb)
	}
}

// 拒绝次序:参数非法在前,实例不存在次之;被拒绝的操作不改变任何状态。
func TestRejectionOrderInvalidBeforeNotFound(t *testing.T) {
	svc := newMigratedService(t)
	// 写入废弃属性 + 目标不存在:必须报参数非法。
	err := svc.Write("ghost", VersionNew, map[string]any{"nick": "x"})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("deprecated prop write to missing instance: want ErrInvalidArgument, got %v", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid-argument must be reported before not-found, got %v", err)
	}
	// 旧版本写入仅存在于新版本的属性:参数非法。
	if err := svc.Write("ghost", VersionOld, map[string]any{"age": 3}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("new-only prop via old write: want ErrInvalidArgument, got %v", err)
	}
	// 合法写入不存在实例:报不存在。
	if err := svc.Write("ghost", VersionNew, map[string]any{"name": "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("write to missing instance: want ErrNotFound, got %v", err)
	}
	// 非法版本号读取:参数非法优先于不存在。
	if _, err := svc.Read("ghost", Version(9)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad version read: want ErrInvalidArgument, got %v", err)
	}
	// 删除不存在实例:不存在。
	if err := svc.Delete("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: want ErrNotFound, got %v", err)
	}
	t.Log("依据: 所有拒绝路径先校验参数合法性, 再检查实例存在性, 两类错误用 ErrInvalidArgument / ErrNotFound 区分")
}

// 被拒绝的迁移声明变更与读写不得改变实例的回填状态与任何版本下的可见数据。
func TestRejectedOpsChangeNothing(t *testing.T) {
	svc := newMigratedService(t)
	if err := svc.Create("a", VersionOld, map[string]any{"name": "ada", "nick": "n"}); err != nil {
		t.Fatal(err)
	}
	oldView := mustRead(t, svc, "a", VersionOld)
	newView := mustRead(t, svc, "a", VersionNew)

	rejected := []error{
		svc.Write("a", VersionNew, map[string]any{"nick": "z"}), // 废弃属性
		svc.Write("a", VersionOld, map[string]any{"age": 1}),    // 旧版本写新增属性
		svc.AmendDeclaration([]migration.Mapping{{Property: "name", Action: migration.ActionRetain}, {Property: "name", Action: migration.ActionDeprecate}}), // 自相矛盾
	}
	for i, err := range rejected {
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("rejected op %d: want ErrInvalidArgument, got %v", i, err)
		}
	}
	if got := mustRead(t, svc, "a", VersionOld); !reflect.DeepEqual(got, oldView) {
		t.Fatalf("old view changed by rejected ops: %v -> %v", oldView, got)
	}
	if got := mustRead(t, svc, "a", VersionNew); !reflect.DeepEqual(got, newView) {
		t.Fatalf("new view changed by rejected ops: %v -> %v", newView, got)
	}
	if _, migrated := svc.Status("a"); migrated {
		t.Fatal("rejected ops must not change backfill state")
	}
}

// 回填即将应用时被正常写入抢先:回填必须识别并跳过,不得覆盖新版本数据。
func TestBackfillSkipsWhenWriteWins(t *testing.T) {
	svc := newMigratedService(t)
	if err := svc.Create("a", VersionOld, map[string]any{"name": "ada", "nick": "n"}); err != nil {
		t.Fatal(err)
	}
	task := svc.PrepareBackfill("a") // 回填已现算好新数据,尚未应用
	if err := svc.Write("a", VersionNew, map[string]any{"name": "grace"}); err != nil {
		t.Fatal(err)
	}
	outcome := task.Apply()
	if outcome != BackfillSkippedStale {
		t.Fatalf("outcome = %v, want skipped-stale", outcome)
	}
	got := mustRead(t, svc, "a", VersionNew)
	want := map[string]any{"name": "grace", "age": 18}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("backfill overwrote concurrent write: got %v, want %v", got, want)
	}
	t.Logf("输入: 回填准备后并发新版本写入 name=grace; 实际输出: outcome=%v, 视图=%v; 依据: 代际号不一致, 回填跳过而非覆盖", outcome, got)
}

// 回填期间实例被删除:回填必须识别并跳过,不得复活已删除的实例。
func TestBackfillSkipsWhenInstanceDeleted(t *testing.T) {
	svc := newMigratedService(t)
	if err := svc.Create("a", VersionOld, map[string]any{"name": "ada"}); err != nil {
		t.Fatal(err)
	}
	task := svc.PrepareBackfill("a")
	if err := svc.Delete("a"); err != nil {
		t.Fatal(err)
	}
	outcome := task.Apply()
	if outcome != BackfillSkippedDeleted {
		t.Fatalf("outcome = %v, want skipped-deleted", outcome)
	}
	if exists, _ := svc.Status("a"); exists {
		t.Fatal("deleted instance was resurrected by backfill")
	}
	if _, err := svc.Read("a", VersionNew); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read after delete: want ErrNotFound, got %v", err)
	}
	t.Logf("输入: 回填准备后删除实例; 实际输出: outcome=%v; 依据: 应用时实例不存在, 跳过且不复活", outcome)
}

// 追加的新对应关系只对尚未回填的实例在其回填时生效,已回填实例不被二次回填。
func TestAmendAffectsOnlyPendingInstances(t *testing.T) {
	svc := newMigratedService(t)
	for _, id := range []string{"a", "b"} {
		if err := svc.Create(id, VersionOld, map[string]any{"name": id}); err != nil {
			t.Fatal(err)
		}
	}
	// 先回填 a。
	if outcome := svc.PrepareBackfill("a").Apply(); outcome != BackfillApplied {
		t.Fatal(outcome)
	}
	// 回填进行中追加新对应关系:新增 score 默认值 100。
	if err := svc.AmendDeclaration([]migration.Mapping{{Property: "score", Action: migration.ActionAddDefault, Default: 100}}); err != nil {
		t.Fatal(err)
	}
	// 再回填 b。
	if outcome := svc.PrepareBackfill("b").Apply(); outcome != BackfillApplied {
		t.Fatal(outcome)
	}
	va := mustRead(t, svc, "a", VersionNew)
	vb := mustRead(t, svc, "b", VersionNew)
	if _, ok := va["score"]; ok {
		t.Fatalf("already-backfilled instance a was re-backfilled with new mapping: %v", va)
	}
	if vb["score"] != 100 {
		t.Fatalf("pending instance b did not pick up appended mapping: %v", vb)
	}
	t.Logf("输入: a 回填后追加 score=100, 再回填 b; 实际输出: a=%v, b=%v; 依据: 追加对应关系只对未回填实例生效", va, vb)
}

// 修改已对回填实例生效的对应关系必须拒绝。
func TestAmendRejectsModifyingEffectiveMapping(t *testing.T) {
	svc := newMigratedService(t)
	if err := svc.Create("a", VersionOld, map[string]any{"name": "ada", "nick": "n"}); err != nil {
		t.Fatal(err)
	}
	if outcome := svc.PrepareBackfill("a").Apply(); outcome != BackfillApplied {
		t.Fatal(outcome)
	}
	err := svc.AmendDeclaration([]migration.Mapping{{Property: "age", Action: migration.ActionAddDefault, Default: 99}})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("modifying effective default: want ErrInvalidArgument, got %v", err)
	}
	err = svc.AmendDeclaration([]migration.Mapping{{Property: "nick", Action: migration.ActionRetain}})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("modifying effective deprecation: want ErrInvalidArgument, got %v", err)
	}
}

// 并发压力:新旧版本读写、删除、回填同时进行,最终状态必须自洽,
// 且每个已回填实例的新旧视图在保留属性上完全一致。
func TestConcurrentReadWriteBackfill(t *testing.T) {
	svc := newMigratedService(t)
	const n = 32
	for i := 0; i < n; i++ {
		if err := svc.Create(fmt.Sprintf("inst-%02d", i), VersionOld, map[string]any{"name": "init", "nick": "n"}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	// 回填 goroutine。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			pending := svc.PendingIDs()
			if len(pending) == 0 {
				return
			}
			svc.PrepareBackfill(pending[0]).Apply()
		}
	}()
	// 读写 goroutine。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := fmt.Sprintf("inst-%02d", (worker+i)%n)
				_ = svc.Write(id, VersionOld, map[string]any{"name": fmt.Sprintf("w%d-%d", worker, i)})
				_, _ = svc.Read(id, VersionNew)
				_, _ = svc.Read(id, VersionOld)
			}
		}(w)
	}
	wg.Wait()
	// 全部实例最终都已回填(写入即回填,回填亦回填)。
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("inst-%02d", i)
		exists, migrated := svc.Status(id)
		if !exists || !migrated {
			t.Fatalf("%s: exists=%v migrated=%v", id, exists, migrated)
		}
		oldView := mustRead(t, svc, id, VersionOld)
		newView := mustRead(t, svc, id, VersionNew)
		if oldView["name"] != newView["name"] {
			t.Fatalf("%s: retained property diverges: old=%v new=%v", id, oldView, newView)
		}
		if newView["age"] != 18 {
			t.Fatalf("%s: default missing in new view: %v", id, newView)
		}
		if _, ok := newView["nick"]; ok {
			t.Fatalf("%s: deprecated property leaked into new view: %v", id, newView)
		}
	}
}
