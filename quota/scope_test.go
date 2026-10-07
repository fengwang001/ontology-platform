package quota

import "testing"

// TestScopeMatchMatrix 覆盖四种作用域的组合与四种 Pod 类别的匹配矩阵。
func TestScopeMatchMatrix(t *testing.T) {
	classes := map[string]PodClass{
		"尽力型/无期限":  {BestEffort: true, Terminating: false},
		"尽力型/有期限":  {BestEffort: true, Terminating: true},
		"非尽力型/无期限": {BestEffort: false, Terminating: false},
		"非尽力型/有期限": {BestEffort: false, Terminating: true},
	}
	cases := []struct {
		name   string
		scopes []Scope
		// 与 classes 的键一一对应的期望匹配结果。
		want map[string]bool
	}{
		{
			name:   "空作用域适用全部",
			scopes: nil,
			want:   map[string]bool{"尽力型/无期限": true, "尽力型/有期限": true, "非尽力型/无期限": true, "非尽力型/有期限": true},
		},
		{
			name:   "尽力型",
			scopes: []Scope{ScopeBestEffort},
			want:   map[string]bool{"尽力型/无期限": true, "尽力型/有期限": true, "非尽力型/无期限": false, "非尽力型/有期限": false},
		},
		{
			name:   "非尽力型",
			scopes: []Scope{ScopeNotBestEffort},
			want:   map[string]bool{"尽力型/无期限": false, "尽力型/有期限": false, "非尽力型/无期限": true, "非尽力型/有期限": true},
		},
		{
			name:   "有期限",
			scopes: []Scope{ScopeTerminating},
			want:   map[string]bool{"尽力型/无期限": false, "尽力型/有期限": true, "非尽力型/无期限": false, "非尽力型/有期限": true},
		},
		{
			name:   "无期限",
			scopes: []Scope{ScopeNotTerminating},
			want:   map[string]bool{"尽力型/无期限": true, "尽力型/有期限": false, "非尽力型/无期限": true, "非尽力型/有期限": false},
		},
		{
			name:   "非尽力型且有期限（全部满足才适用）",
			scopes: []Scope{ScopeNotBestEffort, ScopeTerminating},
			want:   map[string]bool{"尽力型/无期限": false, "尽力型/有期限": false, "非尽力型/无期限": false, "非尽力型/有期限": true},
		},
		{
			name:   "尽力型且无期限",
			scopes: []Scope{ScopeBestEffort, ScopeNotTerminating},
			want:   map[string]bool{"尽力型/无期限": true, "尽力型/有期限": false, "非尽力型/无期限": false, "非尽力型/有期限": false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ss, err := newScopeSet(tc.scopes)
			if err != nil {
				t.Fatalf("作用域 %v 应合法: %v", tc.scopes, err)
			}
			for cname, class := range classes {
				got := ss.matches(class)
				t.Logf("作用域=%v 类别=%s; 输出 matches=%v; 依据 需满足全部作用域", tc.scopes, cname, got)
				if got != tc.want[cname] {
					t.Errorf("作用域=%v 类别=%s: matches=%v, 期望 %v", tc.scopes, cname, got, tc.want[cname])
				}
			}
		})
	}
}

// TestInvalidScopeConfigs 非法作用域配置：矛盾组合与未知作用域。
func TestInvalidScopeConfigs(t *testing.T) {
	cases := []struct {
		name   string
		scopes []Scope
	}{
		{"尽力型与非尽力型矛盾", []Scope{ScopeBestEffort, ScopeNotBestEffort}},
		{"有期限与无期限矛盾", []Scope{ScopeTerminating, ScopeNotTerminating}},
		{"未知作用域", []Scope{Scope("Guaranteed")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newScopeSet(tc.scopes)
			t.Logf("输入 scopes=%v; 输出 err=%v; 依据 矛盾或未知作用域为非法配置", tc.scopes, err)
			if !IsKind(err, ErrInvalidConfiguration) {
				t.Fatalf("期望非法配置, 得到 %v", err)
			}
		})
	}
}

// TestBestEffortQuotaOnlyPods 含尽力型作用域的配额硬上限只允许 pods。
func TestBestEffortQuotaOnlyPods(t *testing.T) {
	_, err := buildQuotaState(Quota{
		Name:   "q",
		Scopes: []Scope{ScopeBestEffort},
		Hard:   map[ResourceName]int64{ResourcePods: 3, "requests.cpu": 2},
	})
	t.Logf("输入 尽力型配额限制 requests.cpu; 输出 err=%v; 依据 尽力型配额硬上限只允许 pods", err)
	if !IsKind(err, ErrInvalidConfiguration) {
		t.Fatalf("期望非法配置, 得到 %v", err)
	}

	qs, err := buildQuotaState(Quota{
		Name:   "q",
		Scopes: []Scope{ScopeBestEffort},
		Hard:   map[ResourceName]int64{ResourcePods: 3},
	})
	if err != nil || qs == nil {
		t.Fatalf("尽力型配额只限 pods 应合法: %v", err)
	}
}

// TestInvalidHardResourceNames 硬上限资源名形式非法。
func TestInvalidHardResourceNames(t *testing.T) {
	for _, r := range []ResourceName{"cpu", "request.cpu", "requests.", "limits.", "Pods", "requests. "} {
		_, err := buildQuotaState(Quota{Name: "q", Hard: map[ResourceName]int64{r: 1}})
		t.Logf("输入 hard 键 %q; 输出 err=%v; 依据 只允许 pods/requests.X/limits.X", r, err)
		if !IsKind(err, ErrInvalidConfiguration) {
			t.Errorf("hard 键 %q 应为非法配置, 得到 %v", r, err)
		}
	}
	for _, r := range []ResourceName{"pods", "requests.cpu", "limits.memory", "requests.nvidia.com/gpu"} {
		if _, err := buildQuotaState(Quota{Name: "q", Hard: map[ResourceName]int64{r: 1}}); err != nil {
			t.Errorf("hard 键 %q 应合法: %v", r, err)
		}
	}
}
