package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// 全局默认规则原子切换：并发读取者只能观察到完整的新版本或完整的旧版本，
// 不允许出现部分属性已切换、部分未切换的中间状态。
func TestConcurrentAtomicDefaultSwitch(t *testing.T) {
	s := NewStore()
	s.AddTenant("tA")
	s.DefineObjectType(ObjectTypeDef{Name: "Doc", Attrs: []string{"title", "body"}})
	v1 := []Statement{
		{ID: "v1t", Scope: attr("title"), Effect: Allow},
		{ID: "v1b", Scope: attr("body"), Effect: Deny},
	}
	v2 := []Statement{
		{ID: "v2t", Scope: attr("title"), Effect: Deny},
		{ID: "v2b", Scope: attr("body"), Effect: Allow},
	}
	if err := s.PutDefault("Doc", "reader", v1); err != nil {
		t.Fatal(err)
	}
	req := AccessRequest{
		SubjectTenant: "tA", SubjectClass: "reader", Type: "Doc",
		InstanceTenant: "tA", Instance: Instance{ID: "i1"},
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errCh := make(chan string, 16)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				dec, err := s.Decide(req)
				if err != nil {
					errCh <- err.Error()
					return
				}
				bt, bb := dec.Trace.Basis["attr:title"], dec.Trace.Basis["attr:body"]
				ok := (bt == "default:v1t" && bb == "default:v1b") ||
					(bt == "default:v2t" && bb == "default:v2b")
				if !ok {
					errCh <- fmt.Sprintf("观察到默认规则切换的中间状态: title=%s body=%s", bt, bb)
					return
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		if i%2 == 0 {
			if err := s.PutDefault("Doc", "reader", v2); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := s.PutDefault("Doc", "reader", v1); err != nil {
				t.Fatal(err)
			}
		}
	}
	close(stop)
	wg.Wait()
	select {
	case msg := <-errCh:
		t.Fatal(msg)
	default:
	}
}

// 撤销覆盖原子性：并发判定只能观察到完整的覆盖集合或完整的默认规则。
func TestConcurrentRevokeAtomicity(t *testing.T) {
	s := NewStore()
	s.AddTenant("tA")
	s.DefineObjectType(ObjectTypeDef{Name: "Doc", Attrs: []string{"title", "body"}})
	def := []Statement{
		{ID: "dT", Scope: attr("title"), Effect: Allow},
		{ID: "dB", Scope: attr("body"), Effect: Deny},
	}
	ovr := []Statement{
		{ID: "oT", Scope: attr("title"), Effect: Deny},
		{ID: "oB", Scope: attr("body"), Effect: Allow},
	}
	if err := s.PutDefault("Doc", "reader", def); err != nil {
		t.Fatal(err)
	}
	if err := s.PutOverride("tA", "Doc", "reader", ovr); err != nil {
		t.Fatal(err)
	}
	req := AccessRequest{
		SubjectTenant: "tA", SubjectClass: "reader", Type: "Doc",
		InstanceTenant: "tA", Instance: Instance{ID: "i1"},
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errCh := make(chan string, 16)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				dec, err := s.Decide(req)
				if err != nil {
					errCh <- err.Error()
					return
				}
				bt, bb := dec.Trace.Basis["attr:title"], dec.Trace.Basis["attr:body"]
				pureDefault := bt == "default:dT" && bb == "default:dB"
				pureOverride := bt == "override:oT" && bb == "override:oB"
				if !pureDefault && !pureOverride {
					errCh <- fmt.Sprintf("观察到撤销的中间状态: title=%s body=%s", bt, bb)
					return
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		if err := s.RevokeOverride("tA", "Doc", "reader"); err != nil {
			t.Fatal(err)
		}
		if err := s.PutOverride("tA", "Doc", "reader", ovr); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	select {
	case msg := <-errCh:
		t.Fatal(msg)
	default:
	}
}

// 混合并发：全局规则变更、租户覆盖变更与判定查询并发执行，
// 审计序号必须连续无空洞（所有成功调用被排入某个全局串行序）。
func TestConcurrentMixedOperations(t *testing.T) {
	s := NewStore()
	for i := 0; i < 4; i++ {
		s.AddTenant(fmt.Sprintf("t%d", i))
	}
	s.DefineObjectType(ObjectTypeDef{Name: "Doc", Attrs: []string{"title", "body"}})
	var wg sync.WaitGroup
	for w := 0; w < 12; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			tenant := fmt.Sprintf("t%d", w%4)
			for i := 0; i < 100; i++ {
				switch (w + i) % 3 {
				case 0:
					_ = s.PutDefault("Doc", "reader", []Statement{
						{ID: fmt.Sprintf("d%d-%d", w, i), Scope: attr("title"), Effect: Effect(i % 2)},
					})
				case 1:
					_ = s.PutOverride(tenant, "Doc", "reader", []Statement{
						{ID: fmt.Sprintf("o%d-%d", w, i), Scope: attr("body"), Effect: Effect(i % 2)},
					})
				default:
					_, _ = s.Decide(AccessRequest{
						SubjectTenant: tenant, SubjectClass: "reader", Type: "Doc",
						InstanceTenant: tenant, Instance: Instance{ID: "i"},
					})
				}
			}
		}(w)
	}
	wg.Wait()
	audit := s.Audit()
	for i, rec := range audit {
		if rec.Seq != uint64(i+1) {
			t.Fatalf("审计序号不连续：位置 %d 的 Seq=%d", i, rec.Seq)
		}
	}
}

// 开销可观测证明：一次判定考察的覆盖规则条目数只与实例归属租户实际登记的
// 覆盖规则数量相关，与系统租户总数及其他租户的覆盖规则数量无关。
func TestExaminedEntriesIndependentOfTenantCount(t *testing.T) {
	for _, tenantCount := range []int{1, 10, 100, 1000} {
		s := NewStore()
		s.DefineObjectType(ObjectTypeDef{Name: "Doc", Attrs: []string{"title", "body"}})
		if err := s.PutDefault("Doc", "reader", []Statement{
			{ID: "d1", Scope: attr("title"), Effect: Allow},
			{ID: "d2", Scope: attr("body"), Effect: Deny},
		}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < tenantCount; i++ {
			tn := fmt.Sprintf("t%d", i)
			s.AddTenant(tn)
			// 其他租户各自登记大量覆盖规则。
			if i > 0 {
				if err := s.PutOverride(tn, "Doc", "reader", []Statement{
					{ID: "x1", Scope: attr("title"), Effect: Deny},
					{ID: "x2", Scope: attr("body"), Effect: Allow},
					{ID: "x3", Scope: Scope{Kind: ScopeAttrWildcard}, Effect: Deny},
				}); err != nil {
					t.Fatal(err)
				}
			}
		}
		// 目标租户 t0 登记 2 条覆盖。
		if err := s.PutOverride("t0", "Doc", "reader", []Statement{
			{ID: "o1", Scope: attr("body"), Effect: Allow},
			{ID: "o2", Scope: attr("title"), Effect: Deny},
		}); err != nil {
			t.Fatal(err)
		}
		dec, err := s.Decide(AccessRequest{
			SubjectTenant: "t0", SubjectClass: "reader", Type: "Doc",
			InstanceTenant: "t0", Instance: Instance{ID: "i"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if dec.Trace.OverrideEntriesExamined != 2 {
			t.Fatalf("租户总数=%d 时考察覆盖条目数=%d，期望恒为 2",
				tenantCount, dec.Trace.OverrideEntriesExamined)
		}
		if dec.Trace.DefaultEntriesExamined != 2 {
			t.Fatalf("租户总数=%d 时考察默认条目数=%d，期望恒为 2",
				tenantCount, dec.Trace.DefaultEntriesExamined)
		}
	}
}
