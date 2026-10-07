package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"time"
)

// naiveModel 是独立实现的朴素全局串行模型：按审计日志的全局序号逐条重放，
// 用一套独立写就的判定逻辑重新推导每次操作的结果，与真实系统记录的结果对照。
type naiveModel struct {
	leaseTTL  time.Duration
	types     map[string]ObjectType
	instances map[InstanceID]*naiveInstance
	heldMax   map[string]InstanceID
	heldSet   map[string]map[InstanceID]bool
	staged    map[string][]Mutation // key: actor|instance|token
}

type naiveOcc struct {
	holder  string
	token   uint64
	expires time.Time
}

type naiveInstance struct {
	typ       string
	version   uint64
	props     map[string]PropertyValue
	occ       *naiveOcc
	lastToken uint64
}

func newNaiveModel(leaseTTL time.Duration, types map[string]ObjectType) *naiveModel {
	return &naiveModel{
		leaseTTL:  leaseTTL,
		types:     types,
		instances: map[InstanceID]*naiveInstance{},
		heldMax:   map[string]InstanceID{},
		heldSet:   map[string]map[InstanceID]bool{},
		staged:    map[string][]Mutation{},
	}
}

func (m *naiveModel) addInstance(id InstanceID, typ string, props map[string]PropertyValue) {
	cp := map[string]PropertyValue{}
	for k, v := range props {
		cp[k] = v
	}
	m.instances[id] = &naiveInstance{typ: typ, version: 1, props: cp}
}

func stageKey(actor string, id InstanceID, token uint64) string {
	return fmt.Sprintf("%s|%s|%d", actor, id, token)
}

func (m *naiveModel) liveOcc(inst *naiveInstance, now time.Time) *naiveOcc {
	if inst.occ != nil && !now.Before(inst.occ.expires) {
		return nil
	}
	return inst.occ
}

func (m *naiveModel) validate(inst *naiveInstance, muts []Mutation) RejectCode {
	t, ok := m.types[inst.typ]
	if !ok {
		return RejectNone
	}
	merged := map[string]PropertyValue{}
	for k, v := range inst.props {
		merged[k] = v
	}
	for _, mu := range muts {
		merged[mu.Property] = mu.Value
	}
	for _, spec := range t.Properties {
		v, present := merged[spec.Name]
		if spec.Required && (!present || v == nil) {
			return RejectCardinality
		}
		if !present || !spec.IsList {
			continue
		}
		list, ok := v.([]any)
		if !ok {
			return RejectCardinality
		}
		if len(list) < spec.Min || (spec.Max >= 0 && len(list) > spec.Max) {
			return RejectCardinality
		}
	}
	return RejectNone
}

func (m *naiveModel) holderValid(inst *naiveInstance, actor string, token uint64, now time.Time) bool {
	occ := inst.occ
	return occ != nil && occ.holder == actor && occ.token == token && now.Before(occ.expires)
}

func (m *naiveModel) release(actor string, id InstanceID, inst *naiveInstance) {
	inst.occ = nil
	set := m.heldSet[actor]
	delete(set, id)
	if len(set) == 0 {
		delete(m.heldSet, actor)
		delete(m.heldMax, actor)
		return
	}
	max := InstanceID("")
	for k := range set {
		if k > max {
			max = k
		}
	}
	m.heldMax[actor] = max
}

// replay 重放一条审计记录，返回模型独立推导出的拒绝码。
func (m *naiveModel) replay(rec AuditRecord) RejectCode {
	now := rec.At
	inst := m.instances[rec.Instance]
	switch rec.Kind {
	case OpUpdate:
		if m.liveOcc(inst, now) != nil {
			return RejectInstanceOccupied
		}
		if code := m.validate(inst, rec.Mutations); code != RejectNone {
			return code
		}
		if rec.ExpectedVersion != inst.version {
			return RejectVersionStale
		}
		for _, mu := range rec.Mutations {
			inst.props[mu.Property] = mu.Value
		}
		inst.version++
		return RejectNone
	case OpAcquire:
		if max, held := m.heldMax[rec.Actor]; held && max > rec.Instance {
			return RejectOrderConflict
		}
		if m.liveOcc(inst, now) != nil {
			return RejectOccupiedByAction
		}
		inst.lastToken++
		inst.occ = &naiveOcc{holder: rec.Actor, token: inst.lastToken, expires: now.Add(m.leaseTTL)}
		set := m.heldSet[rec.Actor]
		if set == nil {
			set = map[InstanceID]bool{}
			m.heldSet[rec.Actor] = set
		}
		set[rec.Instance] = true
		if cur, ok := m.heldMax[rec.Actor]; !ok || rec.Instance > cur {
			m.heldMax[rec.Actor] = rec.Instance
		}
		return RejectNone
	case OpStage:
		if !m.holderValid(inst, rec.Actor, rec.Token, now) {
			return RejectOccupancyLost
		}
		if code := m.validate(inst, rec.Mutations); code != RejectNone {
			return code
		}
		key := stageKey(rec.Actor, rec.Instance, rec.Token)
		m.staged[key] = append(m.staged[key], rec.Mutations...)
		return RejectNone
	case OpHeartbeat:
		if !m.holderValid(inst, rec.Actor, rec.Token, now) {
			return RejectOccupancyLost
		}
		inst.occ.expires = now.Add(m.leaseTTL)
		return RejectNone
	case OpCommit:
		if !m.holderValid(inst, rec.Actor, rec.Token, now) {
			return RejectOccupancyLost
		}
		for _, mu := range m.staged[stageKey(rec.Actor, rec.Instance, rec.Token)] {
			inst.props[mu.Property] = mu.Value
		}
		delete(m.staged, stageKey(rec.Actor, rec.Instance, rec.Token))
		inst.version++
		m.release(rec.Actor, rec.Instance, inst)
		return RejectNone
	case OpAbort:
		if inst.occ != nil && inst.occ.holder == rec.Actor && inst.occ.token == rec.Token {
			m.release(rec.Actor, rec.Instance, inst)
		}
		delete(m.staged, stageKey(rec.Actor, rec.Instance, rec.Token))
		return RejectNone
	}
	return RejectNone
}

// 随机生成的并发操作序列：真实系统执行后，将审计日志逐条重放进朴素串行模型，
// 两者在每一次判定与最终状态上必须完全一致。
func TestRandomizedAgainstNaiveSerialModel(t *testing.T) {
	const (
		numInstances = 5
		numActions   = 6
		numCallers   = 4
		numWorkers   = 8
		opsPerWorker = 120
	)
	leaseTTL := time.Hour // 随机对照中不让租约到期，失效路径由确定性测试覆盖
	typ := ObjectType{
		Name: "Ticket",
		Properties: []PropertySpec{
			{Name: "title", Required: true},
			{Name: "state"},
			{Name: "tags", IsList: true, Min: 0, Max: 3},
		},
	}
	s := NewStore(WithLeaseTTL(leaseTTL))
	s.RegisterType(typ)
	ids := make([]InstanceID, numInstances)
	for i := range ids {
		ids[i] = InstanceID(fmt.Sprintf("I%d", i))
		mustCreate(t, s, ids[i], map[string]PropertyValue{"title": "init", "state": "open"})
	}
	for i := 0; i+1 < numInstances; i++ {
		s.AddLink(Link{Type: "depends", From: ids[i], To: ids[i+1]})
	}

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(1692 + w)))
			held := map[InstanceID]*Occupancy{}
			for op := 0; op < opsPerWorker; op++ {
				id := ids[rng.Intn(numInstances)]
				switch rng.Intn(10) {
				case 0, 1, 2: // 普通乐观更新（含故意落后的版本与偶发基数违规）
					caller := CallerID(fmt.Sprintf("C%d", rng.Intn(numCallers)))
					expected := uint64(rng.Intn(8) + 1)
					mut := Mutation{Property: "title", Value: fmt.Sprintf("w%do%d", w, op)}
					muts := []Mutation{mut}
					if rng.Intn(20) == 0 {
						muts = append(muts, Mutation{Property: "tags", Value: []any{"a", "b", "c", "d"}})
					}
					s.Update(caller, id, expected, muts...)
				case 3, 4, 5: // 占用申请
					if _, ok := held[id]; ok {
						continue
					}
					action := ActionID(fmt.Sprintf("A%d", rng.Intn(numActions)))
					if occ, err := s.Acquire(action, id); err == nil {
						held[id] = occ
					}
				case 6: // 暂存变更
					if occ, ok := held[id]; ok {
						occ.Apply(Mutation{Property: "state", Value: fmt.Sprintf("s%d", op)})
					}
				case 7: // 心跳
					if occ, ok := held[id]; ok {
						occ.Heartbeat()
					}
				case 8: // 提交
					if occ, ok := held[id]; ok {
						occ.Commit()
						delete(held, id)
					}
				case 9: // 中止
					if occ, ok := held[id]; ok {
						occ.Abort()
						delete(held, id)
					}
				}
			}
			for id, occ := range held {
				occ.Abort()
				delete(held, id)
			}
		}(w)
	}
	wg.Wait()

	// 重放核验：模型独立推导的每次判定必须与真实系统记录一致。
	model := newNaiveModel(leaseTTL, map[string]ObjectType{typ.Name: typ})
	for _, id := range ids {
		model.addInstance(id, typ.Name, map[string]PropertyValue{"title": "init", "state": "open"})
	}
	records := s.Audit().Records()
	if len(records) == 0 {
		t.Fatal("no audit records")
	}
	for _, rec := range records {
		got := model.replay(rec)
		if got != rec.Code {
			t.Fatalf("seq %d (%s %s by %s): model=%s recorded=%s basis=%q",
				rec.Seq, rec.Kind, rec.Instance, rec.Actor, got, rec.Code, rec.Basis)
		}
		if rec.Code == RejectNone && rec.Kind != OpHeartbeat && rec.Kind != OpStage && rec.Kind != OpAbort {
			inst := model.instances[rec.Instance]
			if inst.version != rec.NewVersion {
				t.Fatalf("seq %d: model version %d != recorded %d", rec.Seq, inst.version, rec.NewVersion)
			}
		}
	}
	// 最终状态一致：存在以审计序为串行顺序的执行，与并发执行结果等价。
	for _, id := range ids {
		snap, err := s.Get(id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		inst := model.instances[id]
		if snap.Version != inst.version {
			t.Fatalf("%s: store version %d != model %d", id, snap.Version, inst.version)
		}
		if !reflect.DeepEqual(snap.Properties, inst.props) {
			t.Fatalf("%s: store props %v != model %v", id, snap.Properties, inst.props)
		}
	}
	t.Logf("replayed %d audit records, final state matches naive serial model", len(records))
}

// 审计记录必须包含重放所需的完整输入、判定依据与结果。
func TestAuditRecordsComplete(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, "T1", map[string]PropertyValue{"title": "a"})

	s.Update("c1", "T1", 1, Mutation{Property: "title", Value: "b"})
	occ, _ := s.Acquire("A1", "T1")
	s.Update("c2", "T1", 2, Mutation{Property: "title", Value: "c"})
	occ.Apply(Mutation{Property: "state", Value: "closed"})
	occ.Heartbeat()
	occ.Commit()
	s.Update("c3", "T1", 99, Mutation{Property: "title", Value: "d"})

	var lastSeq uint64
	for _, rec := range s.Audit().Records() {
		if rec.Seq <= lastSeq {
			t.Fatalf("seq not strictly increasing at %d", rec.Seq)
		}
		lastSeq = rec.Seq
		if rec.Instance == "" || rec.Actor == "" || rec.Input == "" || rec.Basis == "" {
			t.Fatalf("seq %d: incomplete record %+v", rec.Seq, rec)
		}
		if rec.Kind == OpUpdate && rec.ExpectedVersion == 0 {
			t.Fatalf("seq %d: update missing expected version", rec.Seq)
		}
	}
}
