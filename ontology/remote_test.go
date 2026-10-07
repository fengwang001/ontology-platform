package ontology

import (
	"fmt"
	"testing"
)

// 跨实例：指向实例的钩子声明读取被指向实例的属性，
// 被指向实例在相关属性上的变化必须纳入指向实例下一次写入的判定范围。
func TestRemoteReadSetConflictAcrossLinkedInstances(t *testing.T) {
	s := NewStore()
	s.RegisterObjectType(ObjectType{Name: "Company"})
	s.RegisterObjectType(ObjectType{
		Name: "Employee",
		Hooks: []ValidationHook{{
			Name:        "needs-company-name",
			RemoteReads: []RemoteRead{{Link: "worksAt", TargetType: "Company", Prop: "name"}},
		}},
	})
	s.RegisterLinkType(LinkType{Name: "worksAt", SourceType: "Employee", TargetType: "Company"})
	mustCreate(t, s, "Company", "c1", map[string]string{"name": "ACME", "revenue": "1"})
	mustCreate(t, s, "Employee", "e1", map[string]string{"title": "dev"})
	// 被指向实例在钩子声明的属性上发生变化。
	mustWrite(t, s, "c1", 1, map[string]string{"name": "ACME2"})
	// 指向实例的下一次写入：其相关读集合包含 (Company,name)，
	// 与挂起的远程失效相交，判定为冲突——尽管两个实例之间不存在直接写入。
	err := writeErr(t, s, "e1", 1, map[string]string{"title": "senior"})
	if !IsKind(err, ErrKindPropertyConflict) {
		t.Fatalf("err = %v, want remote property conflict", err)
	}
	we := err.(*WriteError)
	if len(we.RemoteProps) != 1 || we.RemoteProps[0].TargetType != "Company" || we.RemoteProps[0].Prop != "name" {
		t.Fatalf("remote conflict props = %v", we.RemoteProps)
	}
}

// 跨实例对照：被指向实例改动的属性不在钩子声明的读取范围内时，不冲突。
func TestRemoteUnrelatedPropNoConflict(t *testing.T) {
	s := NewStore()
	s.RegisterObjectType(ObjectType{Name: "Company"})
	s.RegisterObjectType(ObjectType{
		Name: "Employee",
		Hooks: []ValidationHook{{
			Name:        "needs-company-name",
			RemoteReads: []RemoteRead{{Link: "worksAt", TargetType: "Company", Prop: "name"}},
		}},
	})
	mustCreate(t, s, "Company", "c1", map[string]string{"name": "ACME", "revenue": "1"})
	mustCreate(t, s, "Employee", "e1", map[string]string{"title": "dev"})
	mustWrite(t, s, "c1", 1, map[string]string{"revenue": "2"})
	r := mustWrite(t, s, "e1", 1, map[string]string{"title": "senior"})
	if r.Version != 2 {
		t.Fatalf("version = %d, want 2", r.Version)
	}
}

// 归属裁定仅依据钩子声明：即使不存在任何链接实例，声明即生效。
func TestRemoteAttributionByHookDeclarationOnly(t *testing.T) {
	s := NewStore()
	s.RegisterObjectType(ObjectType{Name: "Company"})
	s.RegisterObjectType(ObjectType{
		Name: "Employee",
		Hooks: []ValidationHook{{
			Name:        "needs-company-name",
			RemoteReads: []RemoteRead{{Link: "worksAt", TargetType: "Company", Prop: "name"}},
		}},
	})
	// 注意：不注册任何 LinkType，也不建立任何链接实例。
	mustCreate(t, s, "Company", "c1", map[string]string{"name": "ACME"})
	mustCreate(t, s, "Employee", "e1", map[string]string{"title": "dev"})
	mustWrite(t, s, "c1", 1, map[string]string{"name": "ACME2"})
	err := writeErr(t, s, "e1", 1, map[string]string{"title": "senior"})
	if !IsKind(err, ErrKindPropertyConflict) {
		t.Fatalf("err = %v, want conflict driven by hook declaration alone", err)
	}
}

// 被拒绝的写入不得消费挂起的远程失效：拒绝后下一次写入仍须面对同一失效。
func TestRejectedWriteKeepsPendingRemote(t *testing.T) {
	s := NewStore()
	s.RegisterObjectType(ObjectType{Name: "Company"})
	s.RegisterObjectType(ObjectType{
		Name: "Employee",
		Hooks: []ValidationHook{{
			Name:        "needs-company-name",
			RemoteReads: []RemoteRead{{Link: "worksAt", TargetType: "Company", Prop: "name"}},
		}},
	})
	mustCreate(t, s, "Company", "c1", map[string]string{"name": "ACME"})
	mustCreate(t, s, "Employee", "e1", map[string]string{"title": "dev"})
	mustWrite(t, s, "c1", 1, map[string]string{"name": "ACME2"})
	writeErr(t, s, "e1", 1, map[string]string{"title": "senior"})
	// 若拒绝路径消费了 pendingRemote，这次写入会成功；预期仍然冲突。
	err := writeErr(t, s, "e1", 1, map[string]string{"level": "L5"})
	if !IsKind(err, ErrKindPropertyConflict) {
		t.Fatalf("err = %v, want conflict: pendingRemote must survive rejection", err)
	}
}

// 判定开销不随历史版本总数增长：无论历史多长，每次判定只查阅 1 份足迹。
// 证据来自判定日志本身，不依赖额外对外暴露的状态。
func TestConflictCheckCostIndependentOfHistory(t *testing.T) {
	s := NewStore()
	s.RegisterObjectType(ObjectType{Name: "Doc"})
	mustCreate(t, s, "Doc", "d1", map[string]string{"a": "1"})
	const n = 200
	for i := 0; i < n; i++ {
		v, _ := s.Version("d1")
		mustWrite(t, s, "d1", v, map[string]string{fmt.Sprintf("k%d", i): "v"})
	}
	var maxConsulted, lastHistory int
	for _, rec := range s.Decisions() {
		if rec.ObjectID != "d1" {
			continue
		}
		if rec.FootprintsConsulted > maxConsulted {
			maxConsulted = rec.FootprintsConsulted
		}
		lastHistory = rec.HistoryLen
	}
	if lastHistory < n {
		t.Fatalf("history len = %d, want >= %d", lastHistory, n)
	}
	if maxConsulted != 1 {
		t.Fatalf("footprints consulted = %d with history %d, want constant 1", maxConsulted, lastHistory)
	}
}

// 判定日志完整性：每次判定的写集合、相关读集合、裁定结果与依据都被记录。
func TestDecisionLogCompleteness(t *testing.T) {
	s := NewStore()
	s.RegisterObjectType(ObjectType{
		Name: "Doc",
		Hooks: []ValidationHook{{
			Name:      "needs-status",
			ReadProps: []string{"status"},
		}},
	})
	mustCreate(t, s, "Doc", "d1", map[string]string{"title": "a", "status": "draft"})
	mustWrite(t, s, "d1", 1, map[string]string{"title": "b"})
	writeErr(t, s, "d1", 1, map[string]string{"status": "published"})
	recs := s.Decisions()
	if len(recs) != 3 {
		t.Fatalf("decision count = %d, want 3", len(recs))
	}
	for i, rec := range recs {
		if rec.ID != uint64(i+1) {
			t.Fatalf("record %d has ID %d, want dense global order", i, rec.ID)
		}
		if len(rec.WriteSet) == 0 || rec.Reason == "" || rec.Verdict == 0 {
			t.Fatalf("record %d incomplete: %+v", i, rec)
		}
	}
	last := recs[2]
	if last.Verdict != VerdictPropertyConflict {
		t.Fatalf("last verdict = %v, want property-conflict", last.Verdict)
	}
	if fmt.Sprint(last.ReadSet) != "[status]" {
		t.Fatalf("last read set = %v, want [status]", last.ReadSet)
	}
	if fmt.Sprint(last.WriteSet) != "[status]" {
		t.Fatalf("last write set = %v, want [status]", last.WriteSet)
	}
	if last.WriteValues["status"] != "published" {
		t.Fatalf("last write values = %v", last.WriteValues)
	}
}
