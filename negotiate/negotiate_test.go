package negotiate

import (
	"reflect"
	"testing"

	"ontology/adapter"
)

func setupExample(t *testing.T) *Negotiator {
	t.Helper()
	r, err := adapter.New(4)
	if err != nil {
		t.Fatal(err)
	}
	must(t, r.SetAdapter(1, false, true))
	must(t, r.SetAdapter(3, true, false))
	must(t, r.Sunset(3, 100))
	return New(r)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type wantPlan struct {
	v       int
	steps   []int
	req     int
	resp    int
	warning int64
	rev     int
}

func TestNegotiateSpecExamples(t *testing.T) {
	cases := []struct {
		name      string
		rangeText string
		lossy     bool
		scopes    []string
		now       int64
		wantErr   Reason
		errStep   int
		want      *wantPlan
		note      string
	}{
		{"头版本直接服务", "4", false, nil, 50, 0, 0,
			&wantPlan{4, []int{}, 0, 0, -1, 3}, "v=H 无步无告警"},
		{"版本3严格有损", "3", false, nil, 50, ReasonLossy, 3, nil, "首个有损步=3"},
		{"版本3允许有损", "3", true, nil, 50, 0, 0,
			&wantPlan{3, []int{3}, 1, 0, 50, 3}, "告警100-50=50"},
		{"区间1-3严格报top有损", "1-3", false, nil, 50, ReasonLossy, 3, nil,
			"候选2、1无路径但只报top=3的原因"},
		{"区间1-3允许有损取3", "1-3", true, nil, 50, 0, 0,
			&wantPlan{3, []int{3}, 1, 0, 50, 3}, "最高可服务者胜出"},
		{"区间1-2无路径", "1-2", false, nil, 50, ReasonNoPath, 2, nil, "首个缺失步=2"},
		{"未来版本", "5-9", false, nil, 50, ReasonFuture, 0, nil, "lo>H"},
		{"lo恰等于H", "4-1000", true, nil, 50, 0, 0,
			&wantPlan{4, []int{}, 0, 0, -1, 3}, "hi 被夹到 H"},
		{"now=t恰好下线", "3", true, nil, 100, ReasonSunset, 0, nil, "now>=t 即下线"},
		{"now=t-1未下线", "3", true, nil, 99, 0, 0,
			&wantPlan{3, []int{3}, 1, 0, 1, 3}, "告警=100-99"},
		{"下线沿链传递到2", "2", true, nil, 100, ReasonSunset, 0, nil, "候选2经过已下线版本3"},
		{"下线沿链传递到1", "1-2", true, nil, 100, ReasonSunset, 0, nil, "已下线优先于无路径"},
		{"头版本4不受版本3下线影响", "4", false, nil, 100, 0, 0,
			&wantPlan{4, []int{}, 0, 0, -1, 3}, "v=H 不经过任何步"},
		{"响应有损步1只计数", "1", true, nil, 50, ReasonNoPath, 2, nil, "步2仍缺失"},
		{"时间非法负", "3", true, nil, -1, ReasonInvalidTime, 0, nil, "now<0"},
		{"时间非法大", "3", true, nil, 1_000_000_000_000_001, ReasonInvalidTime, 0, nil, "now>1e15"},
		{"时间边界1e15", "4", false, nil, 1_000_000_000_000_000, 0, 0,
			&wantPlan{4, []int{}, 0, 0, -1, 3}, "now=1e15 合法；版本3此时已下线故取4"},
		{"scopes含空串", "3", true, []string{"", "x"}, 50, ReasonInvalidArgument, 0, nil, "参数非法最先"},
		{"语法错误", "03", true, nil, 50, ReasonSyntax, 0, nil, "前导零"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := setupExample(t)
			plan, err := n.Negotiate(c.rangeText, c.lossy, c.scopes, c.now)
			if c.wantErr != 0 {
				got := ReasonOf(err)
				if got != c.wantErr {
					t.Fatalf("Negotiate(%q,...) 类别=%s，期望 %s，依据：%s",
						c.rangeText, got, c.wantErr, c.note)
				}
				var ne *Error
				if asErr := asError(err, &ne); asErr && ne.Step != c.errStep {
					t.Fatalf("错误步号=%d，期望 %d", ne.Step, c.errStep)
				}
				t.Logf("输入range=%q lossy=%v now=%d 输出=%v(步%d) 判定依据=%s",
					c.rangeText, c.lossy, c.now, got, c.errStep, c.note)
				return
			}
			if err != nil {
				t.Fatalf("意外错误 %v，依据：%s", err, c.note)
			}
			if plan.Version != c.want.v ||
				!reflect.DeepEqual(plan.Steps, c.want.steps) ||
				plan.ReqLossy != c.want.req ||
				plan.RespLossy != c.want.resp ||
				plan.Warning != c.want.warning ||
				plan.RegistryRev != c.want.rev {
				t.Fatalf("计划=%+v，期望 {v:%d steps:%v req:%d resp:%d warn:%d rev:%d}，依据：%s",
					plan, c.want.v, c.want.steps, c.want.req, c.want.resp, c.want.warning, c.want.rev, c.note)
			}
			if len(plan.Steps) != 4-plan.Version {
				t.Fatalf("步骤长度=%d，期望 H-v=%d", len(plan.Steps), 4-plan.Version)
			}
			t.Logf("输入range=%q lossy=%v now=%d 输出=v%d steps%v req%d resp%d warn%d rev%d 判定依据=%s",
				c.rangeText, c.lossy, c.now, plan.Version, plan.Steps, plan.ReqLossy,
				plan.RespLossy, plan.Warning, plan.RegistryRev, c.note)
		})
	}
}

func asError(err error, target **Error) bool {
	if e, ok := err.(*Error); ok {
		*target = e
		return true
	}
	return false
}

func TestPreviewSkipsCandidate(t *testing.T) {
	n := setupExample(t)
	must(t, n.registry.Preview(4, "beta"))

	if _, err := n.Negotiate("3-4", false, nil, 50); ReasonOf(err) != ReasonNoPermission {
		t.Fatalf("无 beta 严格模式期望无权限，实际 %v", err)
	}
	plan, err := n.Negotiate("3-4", true, nil, 50)
	if err != nil {
		t.Fatalf("允许有损后应跳过4取3：%v", err)
	}
	if plan.Version != 3 || plan.RegistryRev != 4 {
		t.Fatalf("期望 v=3 rev=4，实际 %+v", plan)
	}
	t.Logf("预览无权限跳过4取次高3，rev=%d", plan.RegistryRev)

	plan, err = n.Negotiate("3-4", false, []string{"beta"}, 50)
	if err != nil {
		t.Fatalf("持有 beta 应得 v=4：%v", err)
	}
	if plan.Version != 4 {
		t.Fatalf("持有作用域期望 v=4，实际 %d", plan.Version)
	}
	t.Logf("持有 beta 得 v=%d", plan.Version)
}

func TestRemoveAndRefillChain(t *testing.T) {
	n := setupExample(t)
	if err := n.registry.RemoveAdapter(1); err != nil {
		t.Fatal(err)
	}
	_, err := n.Negotiate("1", true, nil, 50)
	if ReasonOf(err) != ReasonNoPath {
		t.Fatalf("删除步1后期望无路径(步1)，实际 %v", err)
	}
	var ne *Error
	if !asError(err, &ne) || ne.Step != 1 {
		t.Fatalf("期望首个缺失步=1，实际错误 %v", err)
	}
	must(t, n.registry.SetAdapter(2, false, false))

	plan, err := n.Negotiate("1-3", true, nil, 50)
	if err != nil || plan.Version != 3 {
		t.Fatalf("补齐步2后允许有损仍取 top=3，实际 %+v err=%v", plan, err)
	}
	if _, err := n.Negotiate("1-2", false, nil, 50); ReasonOf(err) != ReasonLossy {
		t.Fatalf("top=2 的链含步3有损，期望有损，实际 %v", err)
	}
	plan, err = n.Negotiate("2-2", true, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != 2 || !reflect.DeepEqual(plan.Steps, []int{2, 3}) ||
		plan.ReqLossy != 1 || plan.RespLossy != 0 || plan.Warning != 50 {
		t.Fatalf("v=2 计划异常：%+v", plan)
	}

	if _, err := n.Negotiate("1-1", true, nil, 50); ReasonOf(err) != ReasonNoPath {
		t.Fatalf("步1已删除，候选1必须仍无路径，实际 %v", err)
	}
	t.Logf("补步2只改变候选2可达性，候选1仍无路径，符合规则")
}

func TestHeadVersionOne(t *testing.T) {
	r, err := adapter.New(1)
	if err != nil {
		t.Fatal(err)
	}
	n := New(r)
	for _, text := range []string{"1", "1-1", "1-1000"} {
		plan, err := n.Negotiate(text, false, nil, 0)
		if err != nil {
			t.Fatalf("H=1 Negotiate(%q) 意外错误 %v", text, err)
		}
		if plan.Version != 1 || len(plan.Steps) != 0 || plan.Warning != -1 {
			t.Fatalf("H=1 期望 v=1 空步 告警-1，实际 %+v", plan)
		}
		t.Logf("H=1 range=%q -> v=1 rev=%d", text, plan.RegistryRev)
	}
	if _, err := n.Negotiate("2-3", false, nil, 0); ReasonOf(err) != ReasonFuture {
		t.Fatalf("H=1 区间2-3应为未来版本，实际 %v", err)
	}
}

func TestNegotiateReadOnly(t *testing.T) {
	n := setupExample(t)
	before := n.registry.Revision()
	for i := 0; i < 5; i++ {
		_, _ = n.Negotiate("1-4", false, nil, 50)
	}
	if after := n.registry.Revision(); after != before {
		t.Fatalf("只读协商改变了版本号：%d -> %d", before, after)
	}
	t.Logf("协商前后版本号保持 %d", before)
}
