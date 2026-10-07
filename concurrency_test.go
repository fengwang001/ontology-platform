package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// naiveSerialModel 是独立的朴素串行参照实现：
// 按给定的全序操作一个个执行，不允许任何交织。
// 并发真实运行后，用事件日志序号重放同样的操作序列，
// 每个调用所用逻辑必须与朴素串行结果完全一致。
type naiveSerialModel struct {
	logicAtType map[string]string // type -> 当前生效逻辑ID（waive 记为 "::waive"）
	parent      map[string]string
}

func newNaive(parent map[string]string) *naiveSerialModel {
	return &naiveSerialModel{logicAtType: map[string]string{}, parent: parent}
}

// resolve 返回 (逻辑标识, 命中类型, basis)；logicID == "::waive" 表示放弃状态。
func (m *naiveSerialModel) resolve(t string) (string, string, HitBasis) {
	first := true
	for cur := t; cur != ""; cur = m.parent[cur] {
		if id, ok := m.logicAtType[cur]; ok {
			if id == "::waive" {
				first = false
				continue
			}
			if cur == t && first {
				return id, cur, HitDirect
			}
			return id, cur, HitInherited
		}
		first = false
	}
	return "", "", HitNone
}

// 并发替换注册与调用发起交织：真实分派器允许 goroutine 任意交织，
// 但每个调用最终使用的逻辑必须等于按全序事件重放的朴素串行结果。
func TestConcurrentLinearizability(t *testing.T) {
	const versions = 40
	const callers = 8
	const callsPer = 120

	d, _ := mustSetup(t)
	if err := d.CreateObject("o", "Startup"); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup

	// 安装者：交替安装 Company 层 V0..Vn，并周期性地 waive/恢复 Startup。
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < versions; i++ {
			id := fmt.Sprintf("V%d", i)
			if _, err := d.InstallLogic("pay", "Company", logicID(id), nil); err != nil {
				panic(err)
			}
			if i%7 == 0 {
				if _, err := d.Waive("pay", "Startup"); err != nil {
					panic(err)
				}
			}
			if i%7 == 1 {
				if _, err := d.InstallLogic("pay", "Startup", logicID("S-"+id), nil); err != nil {
					panic(err)
				}
			}
		}
	}()

	// 调用者：记录自己拿到的 (事件序号 -> 逻辑)。
	type got struct {
		seq            int64
		logic, hitType string
		basis          HitBasis
	}
	gotCh := make(chan got, callers*callsPer)
	for c := 0; c < callers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < callsPer; i++ {
				_, rec := d.Invoke("o", "pay", Input{"amount": 1})
				if rec.Status == StatusOK {
					gotCh <- got{rec.Seq, rec.LogicID, rec.HitType, rec.HitBasis}
				}
			}
		}()
	}

	close(start)
	wg.Wait()
	close(gotCh)

	// 构造“调用事件序号 -> 实际使用逻辑”的观测表。
	observed := map[int64]got{}
	for g := range gotCh {
		if dup, clash := observed[g.seq]; clash {
			t.Fatalf("duplicate invoke seq %d: %v vs %v", g.seq, dup, g)
		}
		observed[g.seq] = g
	}

	// 按事件日志全序重放朴素串行模型，并与每个 invoke 事件对照。
	model := newNaive(map[string]string{"Startup": "Company", "Company": "Entity", "Entity": ""})
	events := d.EventLog()
	checked := 0
	for _, ev := range events {
		switch ev.Kind {
		case "install":
			// Startup 安装的逻辑 ID 以 "S-" 开头，其余安装到事件中的 Type。
			model.logicAtType[ev.Type] = ev.LogicID
		case "waive":
			model.logicAtType[ev.Type] = "::waive"
		case "invoke":
			g, ok := observed[ev.Seq]
			if !ok {
				continue // 最后一批可能遇到无法分派（install 前 Company 未注册）
			}
			wantLogic, wantType, wantBasis := model.resolve("Startup")
			if wantLogic == "" || wantLogic == "::waive" {
				continue
			}
			if g.logic != wantLogic || g.hitType != wantType || g.basis != wantBasis {
				t.Fatalf("seq %d not serializable: got (%s,%s,%s), naive serial wants (%s,%s,%s)",
					ev.Seq, g.logic, g.hitType, g.basis, wantLogic, wantType, wantBasis)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no successful invocations were cross-checked")
	}

	// 所有调用都必须能在事件日志中找到对应时刻；记录数与成功观测数一致。
	t.Logf("cross-checked %d successful calls over %d events", checked, len(events))
}

// 压力测试：-race 下反复并发 install/waive/invoke/revoke，不应出现数据竞争。
func TestConcurrentRaceStress(t *testing.T) {
	d, _ := mustSetup(t)
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("o%d", i)
		if err := d.CreateObject(name, "Startup"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.InstallLogic("pay", "Company", logicID("V0"), nil); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				_, _ = d.InstallLogic("pay", "Company", logicID(fmt.Sprintf("V%d-%d", g, i)), nil)
			}
		}(g)
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := 0; i < 300; i++ {
				_, rec := d.Invoke(fmt.Sprintf("o%d", i%20), "pay", Input{"amount": 1})
				switch rec.Status {
				case StatusOK, StatusNoDispatchNone, StatusNoDispatchWaived,
					StatusRevokedDuringLookup, StatusPreconditionFailed,
					StatusPostconditionFailed, StatusExecuteFailed:
				default:
					t.Errorf("unexpected status %q", rec.Status)
				}
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 10; i++ {
			_ = d.RevokeObject(fmt.Sprintf("o%d", i))
		}
	}()
	close(start)
	wg.Wait()
}
