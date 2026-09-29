package ontology

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mustAdd(t *testing.T, r *Registry, ot *ObjectType) {
	t.Helper()
	if err := r.AddType(ot); err != nil {
		t.Fatalf("AddType(%s) 失败: %v", ot.Name, err)
	}
}

// testTypes 构造一组互相引用的类型：Order 全方位引用 Customer，Shipment 引用 Order。
func testTypes() []*ObjectType {
	return []*ObjectType{
		{
			Name:       "Customer",
			Properties: []Property{{Name: "name", Type: PrimitiveString}},
		},
		{
			Name: "Order",
			Properties: []Property{
				{Name: "id", Type: PrimitiveString},
				{Name: "customer", Type: PrimitiveObjectRef, RefType: "Customer"},
			},
			Links: []LinkType{{Name: "placed_by", SourceType: "Order", TargetType: "Customer"}},
			Actions: []Action{{
				Name:        "escalate",
				Params:      []ActionParam{{Name: "customer", TypeRef: "Customer"}},
				ReturnRefs:  []string{"Customer"},
				PropertyRef: []PropertyRef{{TypeName: "Customer", PropertyName: "name"}},
			}},
		},
		{
			Name: "Shipment",
			Properties: []Property{
				{Name: "order", Type: PrimitiveObjectRef, RefType: "Order"},
			},
			Links: []LinkType{{Name: "fulfills", SourceType: "Shipment", TargetType: "Order"}},
		},
	}
}

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry(discardLogger())
	for _, ot := range testTypes() {
		mustAdd(t, r, ot)
	}
	return r
}

func assertCode(t *testing.T, err error, code Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误码 %s，实际无错误", code)
	}
	oe, ok := err.(*Error)
	if !ok {
		t.Fatalf("期望 *Error，实际 %T: %v", err, err)
	}
	if oe.Code != code {
		t.Fatalf("期望错误码 %s，实际 %s（%v）", code, oe.Code, err)
	}
}

func assertStateEqual(t *testing.T, want, got []*ObjectType, context string) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("%s：状态被改变\nwant: %+v\ngot:  %+v", context, want, got)
	}
}

// 全图依赖更新：属性引用、链接源/目标、Action 参数/返回值/属性引用全部同步改写。
func TestRenameUpdatesAllGraphReferences(t *testing.T) {
	r := newTestRegistry(t)
	if err := r.Rename("Customer", "Client"); err != nil {
		t.Fatalf("Rename 失败: %v", err)
	}

	if got := r.References("Customer"); len(got) != 0 {
		t.Fatalf("重命名后仍残留旧名引用: %+v", got)
	}
	refs := r.References("Client")
	wantKinds := map[string]bool{
		"property_ref": false, "link_target": false,
		"action_param": false, "action_return": false, "action_property_ref": false,
	}
	for _, ref := range refs {
		if _, ok := wantKinds[ref.Kind]; ok {
			wantKinds[ref.Kind] = true
		}
	}
	for kind, seen := range wantKinds {
		if !seen {
			t.Errorf("重命名后缺少 %s 类型的引用更新", kind)
		}
	}

	order, err := r.Resolve("Order")
	if err != nil {
		t.Fatalf("Resolve(Order) 失败: %v", err)
	}
	if order.Properties[1].RefType != "Client" {
		t.Errorf("属性引用未更新: %+v", order.Properties[1])
	}
	if order.Links[0].TargetType != "Client" {
		t.Errorf("链接目标未更新: %+v", order.Links[0])
	}
	act := order.Actions[0]
	if act.Params[0].TypeRef != "Client" || act.ReturnRefs[0] != "Client" || act.PropertyRef[0].TypeName != "Client" {
		t.Errorf("Action 引用未更新: %+v", act)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("重命名后存在悬空引用: %v", err)
	}
}

// 旧名失效：解析、再次重命名、重新注册均被拒绝且原因可区分。
func TestRenameInvalidatesOldName(t *testing.T) {
	r := newTestRegistry(t)
	if err := r.Rename("Customer", "Client"); err != nil {
		t.Fatalf("Rename 失败: %v", err)
	}

	_, err := r.Resolve("Customer")
	assertCode(t, err, CodeOldNameInvalidated)

	assertCode(t, r.Rename("Customer", "Anything"), CodeOldNameInvalidated)
	assertCode(t, r.AddType(&ObjectType{Name: "Customer"}), CodeOldNameInvalidated)
	assertCode(t, r.Rename("Order", "Customer"), CodeOldNameInvalidated)

	if _, err := r.Resolve("Client"); err != nil {
		t.Fatalf("新名应可解析: %v", err)
	}
}

// 原子性：候选状态残留旧名引用时拒绝重命名，注册表状态不变。
func TestRenameRejectsResidualReference(t *testing.T) {
	r := newTestRegistry(t)
	before := r.List()
	r.testCorruptCandidate = func(next map[string]*ObjectType) {
		next["Order"].Properties = append(next["Order"].Properties,
			Property{Name: "ghost", Type: PrimitiveObjectRef, RefType: "Customer"})
	}
	assertCode(t, r.Rename("Customer", "Client"), CodeResidualReference)
	assertStateEqual(t, before, r.List(), "残留旧名引用被拒绝后")
	if _, err := r.Resolve("Customer"); err != nil {
		t.Fatalf("被拒绝的重命名不得改变类型: %v", err)
	}
}

// 原子性：候选状态中旧名仍可解析时拒绝重命名，注册表状态不变。
func TestRenameRejectsOldNameResolvable(t *testing.T) {
	r := newTestRegistry(t)
	before := r.List()
	r.testCorruptCandidate = func(next map[string]*ObjectType) {
		next["Customer"] = next["Client"].clone()
		next["Customer"].Name = "Customer"
	}
	assertCode(t, r.Rename("Customer", "Client"), CodeOldNameResolvable)
	assertStateEqual(t, before, r.List(), "旧名可解析被拒绝后")
}

// 原子性：候选状态出现悬空引用时拒绝重命名，注册表状态不变。
func TestRenameRejectsDanglingReference(t *testing.T) {
	r := newTestRegistry(t)
	before := r.List()
	r.testCorruptCandidate = func(next map[string]*ObjectType) {
		next["Shipment"].Properties[0].RefType = "NonExistent"
	}
	assertCode(t, r.Rename("Customer", "Client"), CodeDanglingReference)
	assertStateEqual(t, before, r.List(), "悬空引用被拒绝后")
}

// 原子性：提交后发现状态损坏（模拟中断）时整体回滚，不留半改状态。
func TestRenameAbortedRollsBack(t *testing.T) {
	r := newTestRegistry(t)
	before := r.List()
	r.testCorruptCommitted = func(committed map[string]*ObjectType) {
		committed["Order"].Links[0].TargetType = "Customer" // 模拟中断留下的半改状态
	}
	assertCode(t, r.Rename("Customer", "Client"), CodeRenameAborted)
	assertStateEqual(t, before, r.List(), "中断回滚后")
	if _, err := r.Resolve("Customer"); err != nil {
		t.Fatalf("回滚后旧名应仍可解析: %v", err)
	}
	assertCode(t, func() error { _, err := r.Resolve("Client"); return err }(), CodeTypeNotFound)
}

// 重名检测：新名被占用、新旧同名均被拒绝，且状态不变。
func TestRenameNameConflict(t *testing.T) {
	r := newTestRegistry(t)
	before := r.List()
	assertCode(t, r.Rename("Customer", "Order"), CodeNameConflict)
	assertCode(t, r.Rename("Customer", "Customer"), CodeNameConflict)
	assertStateEqual(t, before, r.List(), "重名被拒绝后")
}

// 未注册类型重命名返回 TYPE_NOT_FOUND。
func TestRenameTypeNotFound(t *testing.T) {
	r := newTestRegistry(t)
	assertCode(t, r.Rename("Ghost", "Client"), CodeTypeNotFound)
}

// 日志包含类型名、引用位置与判定依据。
func TestRenameLogsTypeReferencesAndReasons(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	r := NewRegistry(logger)
	for _, ot := range testTypes() {
		mustAdd(t, r, ot)
	}
	if err := r.Rename("Customer", "Client"); err != nil {
		t.Fatalf("Rename 失败: %v", err)
	}
	_ = r.Rename("Order", "Customer") // 触发重名/失效判定日志

	out := buf.String()
	for _, want := range []string{
		"Customer", "Client", // 类型名
		"property_ref", "link_target", "action_param", "action_return", "action_property_ref", // 引用位置
		"依据", string(CodeOldNameInvalidated), // 判定依据
	} {
		if !strings.Contains(out, want) {
			t.Errorf("日志缺少 %q\n日志内容:\n%s", want, out)
		}
	}
}

// 并发重命名与读取：任何一致快照上引用都不悬空，最终结果与顺序执行一致。
func TestRenameConcurrent(t *testing.T) {
	const n = 16
	build := func() *Registry {
		r := NewRegistry(discardLogger())
		for i := 0; i < n; i++ {
			ot := &ObjectType{Name: fmt.Sprintf("T%02d", i)}
			if i > 0 {
				ot.Properties = append(ot.Properties,
					Property{Name: "prev", Type: PrimitiveObjectRef, RefType: fmt.Sprintf("T%02d", i-1)})
			}
			if err := r.AddType(ot); err != nil {
				t.Fatalf("AddType 失败: %v", err)
			}
		}
		return r
	}

	r := build()
	var readers, writers sync.WaitGroup
	stop := make(chan struct{})
	for k := 0; k < 4; k++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := r.Validate(); err != nil {
					t.Errorf("并发读取发现悬空引用: %v", err)
					return
				}
			}
		}()
	}
	for i := 0; i < n; i++ {
		writers.Add(1)
		go func(i int) {
			defer writers.Done()
			if err := r.Rename(fmt.Sprintf("T%02d", i), fmt.Sprintf("R%02d", i)); err != nil {
				t.Errorf("并发重命名失败: %v", err)
			}
		}(i)
	}
	writers.Wait()
	close(stop)
	readers.Wait()

	// 与顺序执行的最终状态完全一致。
	seq := build()
	for i := 0; i < n; i++ {
		if err := seq.Rename(fmt.Sprintf("T%02d", i), fmt.Sprintf("R%02d", i)); err != nil {
			t.Fatalf("顺序重命名失败: %v", err)
		}
	}
	assertStateEqual(t, seq.List(), r.List(), "并发与顺序执行结果")
}

// 交换律：同一组重命名以任意顺序执行，最终状态完全相同。
func TestRenameOrderIndependent(t *testing.T) {
	renames := [][2]string{
		{"Customer", "Client"},
		{"Order", "Purchase"},
		{"Shipment", "Delivery"},
	}
	var perms [][][2]string
	var permute func(prefix [][2]string, rest [][2]string)
	permute = func(prefix [][2]string, rest [][2]string) {
		if len(rest) == 0 {
			perms = append(perms, append([][2]string(nil), prefix...))
			return
		}
		for i := range rest {
			next := append([][2]string(nil), rest[:i]...)
			next = append(next, rest[i+1:]...)
			permute(append(append([][2]string(nil), prefix...), rest[i]), next)
		}
	}
	permute(nil, renames)
	if len(perms) != 6 {
		t.Fatalf("期望 6 种排列，实际 %d", len(perms))
	}

	var want []*ObjectType
	for i, order := range perms {
		r := newTestRegistry(t)
		for _, rn := range order {
			if err := r.Rename(rn[0], rn[1]); err != nil {
				t.Fatalf("排列 %d 重命名 %s->%s 失败: %v", i, rn[0], rn[1], err)
			}
		}
		if err := r.Validate(); err != nil {
			t.Fatalf("排列 %d 最终状态存在悬空引用: %v", i, err)
		}
		if want == nil {
			want = r.List()
			continue
		}
		assertStateEqual(t, want, r.List(), fmt.Sprintf("排列 %d 的最终状态", i))
	}
}
