package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naiveModel 是独立实现的朴素全表扫描模型：不维护任何索引，
// 查询时遍历全部实例。用作对拍参照。
type naiveModel struct {
	instances map[string]map[string]Value
}

func newNaiveModel() *naiveModel {
	return &naiveModel{instances: make(map[string]map[string]Value)}
}

func (m *naiveModel) addInstance(id string) {
	m.instances[id] = make(map[string]Value)
}

func (m *naiveModel) write(id, prop string, v Value) bool {
	props, ok := m.instances[id]
	if !ok {
		return false
	}
	props[prop] = v
	return true
}

func (m *naiveModel) batchWrite(prop string, writes []InstanceWrite) bool {
	for _, w := range writes {
		if _, ok := m.instances[w.InstanceID]; !ok {
			return false
		}
	}
	for _, w := range writes {
		m.instances[w.InstanceID][prop] = w.Value
	}
	return true
}

func (m *naiveModel) deleteInstance(id string) bool {
	if _, ok := m.instances[id]; !ok {
		return false
	}
	delete(m.instances, id)
	return true
}

// query 全表扫描：返回当前值等于 v 的实例，开销随实例总数增长（与索引查询对照）。
func (m *naiveModel) query(prop string, v Value) []string {
	var out []string
	for id, props := range m.instances {
		val, ok := props[prop]
		if !ok {
			val = Absent
		}
		if val == v {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// TestDifferentialRandomOps 随机生成写入/批量/删除/查询序列，
// 与朴素全表扫描模型对拍；日志打印每步输入、索引变化与判定依据。
func TestDifferentialRandomOps(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	log := NewBufferLogger()
	faults := NewFaultInjector()
	exact := NewIndex("status-exact", ExactKey)
	prefix := NewIndex("status-prefix", PrefixKey(1))
	s := NewStore(NewMemoryWAL(), faults, log)
	s.RegisterProperty("status", exact, prefix)
	model := newNaiveModel()
	ids := []string{"A", "B", "C", "D", "E", "F"}
	values := []Value{Of(""), Of("active"), Of("paused"), Of("archived"), Absent}
	alive := make(map[string]bool)
	for _, id := range ids {
		s.AddInstance(id)
		model.addInstance(id)
		alive[id] = true
	}
	checkQuery := func(step int, v Value) {
		got, err := s.QueryByValue("status", v)
		if err != nil {
			t.Fatalf("step %d: query %+v err %v", step, v, err)
		}
		want := model.query("status", v)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("step %d: query %+v store=%v model=%v", step, v, got, want)
		}
	}
	const steps = 800
	for step := 0; step < steps; step++ {
		op := rng.Intn(100)
		switch {
		case op < 35: // 单实例写入
			id := ids[rng.Intn(len(ids))]
			v := values[rng.Intn(len(values))]
			err := s.Write(id, "status", v)
			ok := model.write(id, "status", v)
			if (err == nil) != ok {
				t.Fatalf("step %d: write %s=%+v store-err=%v model-ok=%v", step, id, v, err, ok)
			}
		case op < 45: // 批量写入（可能整体回滚）
			var ws []InstanceWrite
			n := 1 + rng.Intn(3)
			for i := 0; i < n; i++ {
				ws = append(ws, InstanceWrite{
					InstanceID: ids[rng.Intn(len(ids))],
					Value:      values[rng.Intn(len(values))],
				})
			}
			errs := s.BatchWrite("status", ws)
			ok := model.batchWrite("status", ws)
			if ok {
				for i, e := range errs {
					if e != nil {
						t.Fatalf("step %d: batch item %d unexpected err %v", step, i, e)
					}
				}
			} else {
				// 模型拒绝（有实例不存在）：存储必须整体拒绝且逐实例报错。
				for i, e := range errs {
					if e == nil {
						t.Fatalf("step %d: batch item %d should fail", step, i)
					}
				}
			}
		case op < 55: // 删除实例
			id := ids[rng.Intn(len(ids))]
			err := s.DeleteInstance(id)
			ok := model.deleteInstance(id)
			if (err == nil) != ok {
				t.Fatalf("step %d: delete %s store-err=%v model-ok=%v", step, id, err, ok)
			}
			if ok {
				alive[id] = false
			}
		case op < 60: // 重建已删除实例
			id := ids[rng.Intn(len(ids))]
			if !alive[id] {
				s.AddInstance(id)
				model.addInstance(id)
				alive[id] = true
			}
		case op < 65: // 偶发崩溃 + 恢复
			point := CutPoint(rng.Intn(int(CutAfterCommit) + 1))
			faults.CrashWhen(crashOnce(point))
			id := ids[rng.Intn(len(ids))]
			v := values[rng.Intn(len(values))]
			func() {
				defer func() { recover() }()
				_ = s.Write(id, "status", v)
			}()
			faults.CrashWhen(nil)
			decisions := s.Recover()
			// 崩溃后的写入是否生效取决于恢复判定；按判定同步模型。
			applied := false
			for _, d := range decisions {
				if d.Decision == "rolled-forward" {
					applied = true
				}
			}
			if applied {
				model.write(id, "status", v)
			}
			log.Logf("DIFF step=%d crash-point=%s decisions=%+v", step, point, decisions)
		default: // 查询对拍
			checkQuery(step, values[rng.Intn(len(values))])
		}
	}
	// 终态全量对拍：每个可能值的查询结果都必须一致。
	for _, v := range values {
		checkQuery(steps, v)
	}
	t.Logf("differential log excerpt (last 40 lines):")
	lines := log.Lines()
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}
	for _, l := range lines {
		t.Log(l)
	}
}
