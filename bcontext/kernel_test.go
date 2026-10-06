package bcontext

import (
	"errors"
	"io"
	"sync"
	"testing"
)

func testKernel(t *testing.T) *Kernel {
	t.Helper()
	return NewKernel(map[string]Feature{
		"camera":   {Default: DefaultSelf},
		"geo":      {Default: DefaultAll},
		"isolated": {RequiresIsolation: true, Default: DefaultAll},
	}, NewLogger(io.Discard))
}

func doc(origin string, opener OpenerPolicy, embedder EmbedderPolicy) Document {
	return Document{Origin: origin, Opener: opener, Embedder: embedder}
}

func mustID2(t *testing.T) func(int64, error) int64 {
	return func(id int64, err error) int64 {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return id
	}
}

func wantErr(t *testing.T, got, target error) {
	t.Helper()
	if !errors.Is(got, target) {
		t.Fatalf("want %v, got %v", target, got)
	}
}

// 顶层两条策略逐一缺失：缺同源开启者策略或缺严格嵌入者策略均不隔离。
func TestTopLevelIsolationRequirements(t *testing.T) {
	k := testKernel(t)
	cases := []struct {
		doc  Document
		want bool
	}{
		{doc("https://a", OpenerSameOrigin, EmbedderRequireCorp), true},
		{doc("https://a", OpenerSameOrigin, EmbedderCredentialless), true},
		{doc("https://a", OpenerSameOriginAllowPopups, EmbedderRequireCorp), false},
		{doc("https://a", OpenerUnsafeNone, EmbedderRequireCorp), false},
		{doc("https://a", OpenerSameOrigin, EmbedderUnsafeNone), false},
	}
	for i, tc := range cases {
		id := mustID2(t)(k.NewTopLevel(tc.doc))
		got, err := k.Isolated(id)
		if err != nil || got != tc.want {
			t.Fatalf("case %d: isolated=%v want %v err=%v", i, got, tc.want, err)
		}
	}
}

// 后代声明齐全但顶层不隔离：后代一律不隔离；父导航替换子树。
func TestNonIsolatedTopTaintsDescendants(t *testing.T) {
	k := testKernel(t)
	top := mustID2(t)(k.NewTopLevel(doc("https://top", OpenerUnsafeNone, EmbedderUnsafeNone)))
	child := mustID2(t)(k.LoadFrame(top, doc("https://child", OpenerSameOrigin, EmbedderRequireCorp), nil))
	grand := mustID2(t)(k.LoadFrame(child, doc("https://g", OpenerSameOrigin, EmbedderCredentialless), nil))
	for _, id := range []int64{child, grand} {
		if iso, _ := k.Isolated(id); iso {
			t.Fatalf("descendant %d isolated under non-isolated top", id)
		}
	}
	if err := k.Navigate(top, doc("https://top", OpenerSameOrigin, EmbedderRequireCorp)); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Isolated(child); !errors.Is(err, ErrDocumentNotExist) {
		t.Fatalf("replaced subtree: want ErrDocumentNotExist, got %v", err)
	}
	if _, err := k.Isolated(grand); !errors.Is(err, ErrDocumentNotExist) {
		t.Fatalf("grandchild in replaced subtree: want ErrDocumentNotExist, got %v", err)
	}
	child2 := mustID2(t)(k.LoadFrame(top, doc("https://c2", OpenerUnsafeNone, EmbedderRequireCorp), nil))
	if iso, _ := k.Isolated(child2); !iso {
		t.Fatal("new descendant under isolated top should be isolated with strict embedder")
	}
}

// 嵌入准入：严格嵌入者的父只限制跨源子，不限制同源子；拒绝不改变状态。
func TestEmbedAdmission(t *testing.T) {
	k := testKernel(t)
	top := mustID2(t)(k.NewTopLevel(doc("https://a", OpenerSameOrigin, EmbedderRequireCorp)))
	if _, err := k.LoadFrame(top, doc("https://a", OpenerUnsafeNone, EmbedderUnsafeNone), nil); err != nil {
		t.Fatalf("same-origin child must be admitted regardless of policy: %v", err)
	}
	_, err := k.LoadFrame(top, doc("https://b", OpenerUnsafeNone, EmbedderUnsafeNone), nil)
	wantErr(t, err, ErrEmbedderMismatch)
	crossStrict := mustID2(t)(k.LoadFrame(top, doc("https://c", OpenerUnsafeNone, EmbedderCredentialless), nil))
	// 父自身严格（无凭据），跨源子仍必须严格：被拒。
	if _, err := k.LoadFrame(crossStrict, doc("https://d", OpenerUnsafeNone, EmbedderUnsafeNone), nil); !errors.Is(err, ErrEmbedderMismatch) {
		t.Fatalf("strict embedder parent must reject cross-origin non-strict child: %v", err)
	}
	// 非严格父（另一棵树）可自由嵌入跨源子。
	loose := mustID2(t)(k.NewTopLevel(doc("https://l", OpenerUnsafeNone, EmbedderUnsafeNone)))
	if _, err := k.LoadFrame(loose, doc("https://d", OpenerUnsafeNone, EmbedderUnsafeNone), nil); err != nil {
		t.Fatalf("non-strict parent must admit any cross-origin child: %v", err)
	}
	before, _ := k.Isolated(crossStrict)
	if err := k.Navigate(crossStrict, doc("https://e", OpenerUnsafeNone, EmbedderUnsafeNone)); !errors.Is(err, ErrEmbedderMismatch) {
		t.Fatalf("cross-origin nav to non-strict doc under strict parent: want mismatch, got %v", err)
	}
	after, _ := k.Isolated(crossStrict)
	if before != after {
		t.Fatal("rejected navigation must not change derived state")
	}
}

func checkRef(t *testing.T, ok bool, err error, want bool) {
	t.Helper()
	if want {
		if err != nil || !ok {
			t.Fatalf("want reachable, got ok=%v err=%v", ok, err)
		}
		return
	}
	if !errors.Is(err, ErrOpenerGroupBroken) {
		t.Fatalf("want ErrOpenerGroupBroken, got ok=%v err=%v", ok, err)
	}
}

// 三种开启者策略下的分组与断开；断开不随后续导航恢复。
func TestOpenerGrouping(t *testing.T) {
	type ref struct{ fwd, back bool }
	cases := []struct {
		name      string
		openerDoc Document
		openeeDoc Document
		want      ref
	}{
		{"none opener cross-origin", doc("https://a", OpenerUnsafeNone, EmbedderUnsafeNone), doc("https://b", OpenerUnsafeNone, EmbedderUnsafeNone), ref{true, true}},
		{"allow-popups opener cross-origin openee", doc("https://a", OpenerSameOriginAllowPopups, EmbedderUnsafeNone), doc("https://b", OpenerUnsafeNone, EmbedderUnsafeNone), ref{true, true}},
		{"allow-popups opener with same-origin openee", doc("https://a", OpenerSameOriginAllowPopups, EmbedderUnsafeNone), doc("https://b", OpenerSameOrigin, EmbedderUnsafeNone), ref{true, false}},
		{"same-origin opener cuts cross-origin both ways", doc("https://a", OpenerSameOrigin, EmbedderUnsafeNone), doc("https://b", OpenerUnsafeNone, EmbedderUnsafeNone), ref{false, false}},
		{"same-origin opener same origin keeps both", doc("https://a", OpenerSameOrigin, EmbedderUnsafeNone), doc("https://a", OpenerSameOrigin, EmbedderUnsafeNone), ref{true, true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := testKernel(t)
			opener := mustID2(t)(k.NewTopLevel(tc.openerDoc))
			openee := mustID2(t)(k.OpenPopup(opener, tc.openeeDoc))
			ok, err := k.CrossReference(opener, openee)
			checkRef(t, ok, err, tc.want.fwd)
			ok, err = k.CrossReference(openee, opener)
			checkRef(t, ok, err, tc.want.back)
			// 被开启者在自己来源内导航到兼容文档：已断开方向不恢复。
			if err := k.Navigate(openee, doc(tc.openeeDoc.Origin, OpenerUnsafeNone, EmbedderUnsafeNone)); err != nil {
				t.Fatal(err)
			}
			ok, err = k.CrossReference(opener, openee)
			checkRef(t, ok, err, tc.want.fwd)
			ok, err = k.CrossReference(openee, opener)
			checkRef(t, ok, err, tc.want.back)
		})
	}
}

// 开启者后来导航到跨源且自身同源策略，也会重新断开组边。
func TestOpenerNavigationResevers(t *testing.T) {
	k := testKernel(t)
	opener := mustID2(t)(k.NewTopLevel(doc("https://a", OpenerSameOriginAllowPopups, EmbedderUnsafeNone)))
	openee := mustID2(t)(k.OpenPopup(opener, doc("https://b", OpenerUnsafeNone, EmbedderUnsafeNone)))
	if ok, _ := k.CrossReference(opener, openee); !ok {
		t.Fatal("group should initially be intact")
	}
	if err := k.Navigate(opener, doc("https://c", OpenerSameOrigin, EmbedderUnsafeNone)); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CrossReference(opener, openee); !errors.Is(err, ErrOpenerGroupBroken) {
		t.Fatalf("after cross-origin same-origin-policy navigation group must break, got %v", err)
	}
}

// 能力三条件逐一缺失，默认列表两种配置下同源/跨源差别。
func TestFeatureConditionsAndDefaults(t *testing.T) {
	// 条件1：父文档能力不可用，子不可用。
	k := testKernel(t)
	top := mustID2(t)(k.NewTopLevel(Document{
		Origin: "https://a", Opener: OpenerSameOrigin, Embedder: EmbedderRequireCorp,
		Permissions: map[string][]string{"camera": {}},
	}))
	child := mustID2(t)(k.LoadFrame(top, doc("https://b", OpenerUnsafeNone, EmbedderRequireCorp), map[string][]string{
		"camera": {"https://b"},
	}))
	if ok, _ := k.Enabled(child, "camera"); ok {
		t.Fatal("parent lacks camera: child must be disabled")
	}

	// 条件2：父已开，但默认仅自身的嵌入列表不含跨源子来源。
	k = testKernel(t)
	top = mustID2(t)(k.NewTopLevel(Document{
		Origin: "https://a", Opener: OpenerSameOrigin, Embedder: EmbedderRequireCorp,
		Permissions: map[string][]string{"camera": {"https://a"}},
	}))
	child = mustID2(t)(k.LoadFrame(top, doc("https://b", OpenerUnsafeNone, EmbedderRequireCorp), nil))
	if ok, _ := k.Enabled(child, "camera"); ok {
		t.Fatal("default-self allowlist must deny cross-origin child")
	}
	sameOrigin := mustID2(t)(k.LoadFrame(top, doc("https://a", OpenerUnsafeNone, EmbedderRequireCorp), nil))
	if ok, _ := k.Enabled(sameOrigin, "camera"); !ok {
		t.Fatal("default-self allowlist must allow same-origin child")
	}
	// 默认全部来源：跨源子默认可用（三条件中自身声明默认允许全部）。
	if ok, _ := k.Enabled(child, "geo"); !ok {
		t.Fatal("default-all feature must allow cross-origin child")
	}

	// 条件3：子自身声明排除自身来源。
	k = testKernel(t)
	top = mustID2(t)(k.NewTopLevel(Document{
		Origin: "https://a", Opener: OpenerSameOrigin, Embedder: EmbedderRequireCorp,
		Permissions: map[string][]string{"camera": {"https://a"}},
	}))
	child = mustID2(t)(k.LoadFrame(top, Document{
		Origin: "https://b", Opener: OpenerUnsafeNone, Embedder: EmbedderRequireCorp,
		Permissions: map[string][]string{"camera": {"https://other"}},
	}, map[string][]string{"camera": {"https://b"}}))
	if ok, _ := k.Enabled(child, "camera"); ok {
		t.Fatal("child policy excluding its own origin must disable")
	}

	// 三条件齐备：可用，且中间层缺一也不可用（孙级）。
	k = testKernel(t)
	top = mustID2(t)(k.NewTopLevel(Document{
		Origin: "https://a", Opener: OpenerSameOrigin, Embedder: EmbedderRequireCorp,
		Permissions: map[string][]string{"camera": {"https://a"}},
	}))
	child = mustID2(t)(k.LoadFrame(top, Document{
		Origin: "https://b", Opener: OpenerUnsafeNone, Embedder: EmbedderRequireCorp,
		Permissions: map[string][]string{"camera": {"https://b"}},
	}, map[string][]string{"camera": {"https://b"}}))
	if ok, _ := k.Enabled(child, "camera"); !ok {
		t.Fatal("all three conditions hold: should be enabled")
	}
	// 父对孙级嵌入列表无该能力，默认仅自身：跨源孙来源被中间层拒绝。
	// 孙文档使用无策略嵌入者：父（严格）本应拒绝，但这里父不是严格嵌入者，
	// 故可装载；隔离与否不影响 camera（非隔离门控能力）。
	grand := mustID2(t)(k.LoadFrame(child, Document{
		Origin: "https://c", Opener: OpenerUnsafeNone, Embedder: EmbedderRequireCorp,
		Permissions: map[string][]string{"camera": {"https://c"}},
	}, nil))
	if ok, _ := k.Enabled(grand, "camera"); ok {
		t.Fatal("child embedding allowlist omits camera and defaults to self only: cross-origin grandchild denied")
	}
}

// 需隔离能力在隔离/未隔离的顶层与后代四种组合下的行为。
func TestIsolationGatedFeature(t *testing.T) {
	k := testKernel(t)
	isoTop := mustID2(t)(k.NewTopLevel(Document{
		Origin: "https://a", Opener: OpenerSameOrigin, Embedder: EmbedderRequireCorp,
		Permissions: map[string][]string{"isolated": {"*"}},
	}))
	nonIsoTop := mustID2(t)(k.NewTopLevel(Document{
		Origin: "https://b", Opener: OpenerUnsafeNone, Embedder: EmbedderUnsafeNone,
		Permissions: map[string][]string{"isolated": {"*"}},
	}))
	isoChild := mustID2(t)(k.LoadFrame(isoTop, doc("https://c", OpenerUnsafeNone, EmbedderRequireCorp), map[string][]string{"isolated": {"*"}}))
	nonIsoChild := mustID2(t)(k.LoadFrame(nonIsoTop, doc("https://d", OpenerUnsafeNone, EmbedderUnsafeNone), map[string][]string{"isolated": {"*"}}))
	if ok, _ := k.Enabled(isoTop, "isolated"); !ok {
		t.Fatal("isolated top should enable gated feature")
	}
	if ok, _ := k.Enabled(nonIsoTop, "isolated"); ok {
		t.Fatal("non-isolated top must disable gated feature despite allow")
	}
	if ok, _ := k.Enabled(isoChild, "isolated"); !ok {
		t.Fatal("isolated descendant should enable gated feature")
	}
	if ok, _ := k.Enabled(nonIsoChild, "isolated"); ok {
		t.Fatal("non-isolated descendant must disable gated feature")
	}
}

// 子框架导航后重推导而父链不变；允许列表修改只影响此后装载。
func TestNavigationAndAllowlistUpdate(t *testing.T) {
	k := testKernel(t)
	top := mustID2(t)(k.NewTopLevel(Document{
		Origin: "https://a", Opener: OpenerSameOrigin, Embedder: EmbedderRequireCorp,
		Permissions: map[string][]string{"camera": {"https://a"}},
	}))
	child := mustID2(t)(k.LoadFrame(top, doc("https://a", OpenerUnsafeNone, EmbedderUnsafeNone), nil))
	if iso, _ := k.Isolated(child); iso {
		t.Fatal("non-strict embedder child under isolated top is not isolated")
	}
	// 导航到严格文档：隔离按新头重推导，父链不变。
	if err := k.Navigate(child, Document{
		Origin: "https://b", Opener: OpenerUnsafeNone, Embedder: EmbedderRequireCorp,
		Permissions: map[string][]string{"camera": {"https://b"}},
	}); err != nil {
		t.Fatal(err)
	}
	if iso, _ := k.Isolated(child); !iso {
		t.Fatal("isolation must be re-derived from new headers after navigation")
	}
	// 修改允许列表（只含 other）：不影响当前文档，但下次装载生效。
	if err := k.SetFrameAllowlist(child, map[string][]string{"camera": {"https://other"}}); err != nil {
		t.Fatal(err)
	}
	if err := k.Navigate(child, Document{
		Origin: "https://b2", Opener: OpenerUnsafeNone, Embedder: EmbedderRequireCorp,
		Permissions: map[string][]string{"camera": {"https://b2"}},
	}); err != nil {
		t.Fatal(err)
	}
	// 当前非隔离文档（https://b）阶段其 camera 因默认自身列表跨源本就不可用；
	// 这里验证装载后再次修改列表不影响已装载文档：
	if ok, _ := k.Enabled(child, "camera"); ok {
		t.Fatal("load-time snapshot allowlist excludes https://b2: camera denied")
	}
	if err := k.SetFrameAllowlist(child, map[string][]string{"camera": {"https://b2"}}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := k.Enabled(child, "camera"); ok {
		t.Fatal("editing the allowlist after load must not affect the already-loaded document")
	}
	// 重新装载后新允许列表生效，三条件齐备。
	if err := k.Navigate(child, Document{
		Origin: "https://b2", Opener: OpenerUnsafeNone, Embedder: EmbedderRequireCorp,
		Permissions: map[string][]string{"camera": {"https://b2"}},
	}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := k.Enabled(child, "camera"); !ok {
		t.Fatal("allowlist updated to include https://b2 before re-navigation: should be enabled")
	}
}

// 拒绝次序：参数非法 > 上下文不存在 > 文档不存在 > 嵌入策略不符 > 组已断开 > 能力未知。
func TestRejectionOrder(t *testing.T) {
	k := testKernel(t)
	// 未知策略值（参数非法）先于不存在的上下文。
	_, err := k.LoadFrame(999, doc("", OpenerPolicy(99), EmbedderPolicy(99)), nil)
	wantErr(t, err, ErrInvalidArgument)
	if _, err := k.Enabled(999, ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty feature name invalid before ctx lookup: %v", err)
	}
	if _, err := k.Isolated(999); !errors.Is(err, ErrContextNotExist) {
		t.Fatalf("missing ctx: %v", err)
	}
	top := mustID2(t)(k.NewTopLevel(doc("https://a", OpenerSameOrigin, EmbedderRequireCorp)))
	child := mustID2(t)(k.LoadFrame(top, doc("https://b", OpenerUnsafeNone, EmbedderRequireCorp), nil))
	if err := k.Navigate(top, doc("https://a", OpenerSameOrigin, EmbedderRequireCorp)); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Enabled(child, "camera"); !errors.Is(err, ErrDocumentNotExist) {
		t.Fatalf("detached doc before unknown feature: %v", err)
	}
	if _, err := k.Enabled(child, "nope"); !errors.Is(err, ErrDocumentNotExist) {
		t.Fatalf("document-not-exist precedes unknown feature: %v", err)
	}
	// 嵌入策略不符先于能力未知：LoadFrame 阶段不识别能力，故用导航与查询顺序佐证。
	aliveChild := mustID2(t)(k.LoadFrame(top, doc("https://b", OpenerUnsafeNone, EmbedderRequireCorp), nil))
	if err := k.Navigate(aliveChild, doc("https://x", OpenerUnsafeNone, EmbedderUnsafeNone)); !errors.Is(err, ErrEmbedderMismatch) {
		t.Fatalf("embed mismatch on navigation: %v", err)
	}
	if _, err := k.Enabled(aliveChild, "nope"); !errors.Is(err, ErrFeatureUnknown) {
		t.Fatalf("alive doc unknown feature: %v", err)
	}
	// 开启者组已断开先于能力未知。
	opener := mustID2(t)(k.NewTopLevel(doc("https://p", OpenerSameOrigin, EmbedderUnsafeNone)))
	openee := mustID2(t)(k.OpenPopup(opener, doc("https://q", OpenerUnsafeNone, EmbedderUnsafeNone)))
	if _, err := k.CrossReference(openee, opener); !errors.Is(err, ErrOpenerGroupBroken) {
		t.Fatalf("group broken: %v", err)
	}
}

// 并发调用等价于某串行顺序：不变量“子可用则父可用”始终成立。
func TestConcurrentSerializability(t *testing.T) {
	k := testKernel(t)
	top := mustID2(t)(k.NewTopLevel(Document{
		Origin: "https://a", Opener: OpenerSameOrigin, Embedder: EmbedderRequireCorp,
		Permissions: map[string][]string{"camera": {"https://a"}, "isolated": {"*"}},
	}))
	frames := make([]int64, 16)
	for i := range frames {
		frames[i] = mustID2(t)(k.LoadFrame(top, Document{
			Origin: "https://b", Opener: OpenerUnsafeNone, Embedder: EmbedderRequireCorp,
			Permissions: map[string][]string{"camera": {"https://b"}, "isolated": {"*"}},
		}, map[string][]string{"camera": {"https://b"}, "isolated": {"*"}}))
	}
	var wg sync.WaitGroup
	for round := 0; round < 8; round++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for _, id := range frames {
				_ = k.Navigate(id, Document{
					Origin: "https://b", Opener: OpenerUnsafeNone, Embedder: EmbedderRequireCorp,
					Permissions: map[string][]string{"camera": {"https://b"}, "isolated": {"*"}},
				})
			}
		}()
		go func() {
			defer wg.Done()
			for _, id := range frames {
				_, _ = k.Enabled(id, "camera")
				_, _ = k.Enabled(id, "isolated")
			}
		}()
		go func() {
			defer wg.Done()
			_ = k.SetFrameAllowlist(frames[0], map[string][]string{"camera": {"https://b"}, "isolated": {"*"}})
		}()
	}
	wg.Wait()
	for _, id := range frames {
		if childOK, _ := k.Enabled(id, "camera"); childOK {
			if parentOK, _ := k.Enabled(top, "camera"); !parentOK {
				t.Fatalf("invariant violated: feature enabled in child %d but not parent", id)
			}
		}
	}
}
func mustID2B(b *testing.B) func(int64, error) int64 {
	return func(id int64, err error) int64 {
		b.Helper()
		if err != nil {
			b.Fatal(err)
		}
		return id
	}
}

// buildDeepTree 构造深度 depth 的单链，外加 bush 个与查询无关的旁支顶层树。
func buildDeepTree(b *testing.B, depth, bush int) (*Kernel, []int64) {
	b.Helper()
	k := NewKernel(map[string]Feature{"camera": {Default: DefaultSelf}}, NewLogger(io.Discard))
	topPerm := Document{Origin: "https://o0", Opener: OpenerSameOrigin, Embedder: EmbedderRequireCorp,
		Permissions: map[string][]string{"camera": {"https://o0"}}}
	top := mustID2B(b)(k.NewTopLevel(topPerm))
	chain := []int64{top}
	parent := top
	for i := 1; i < depth; i++ {
		origin := "https://o" + itoa(i)
		node := mustID2B(b)(k.LoadFrame(parent, Document{
			Origin: origin, Opener: OpenerUnsafeNone, Embedder: EmbedderRequireCorp,
			Permissions: map[string][]string{"camera": {origin}},
		}, map[string][]string{"camera": {origin}}))
		chain = append(chain, node)
		parent = node
	}
	// 与被查链无关的大量节点：O(深度) 与 O(1) 的查询都不应受其影响。
	for i := 0; i < bush; i++ {
		t := mustID2B(b)(k.NewTopLevel(topPerm))
		mustID2B(b)(k.LoadFrame(t, doc("https://x", OpenerUnsafeNone, EmbedderRequireCorp), nil))
	}
	return k, chain
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// BenchmarkIsolated 证明隔离查询 O(1)：浅层与深层、小树与大树耗时应基本一致。
func BenchmarkIsolated(b *testing.B) {
	for _, cfg := range []struct{ depth, bush int }{{8, 64}, {4096, 64}, {4096, 4096}} {
		k, chain := buildDeepTree(b, cfg.depth, cfg.bush)
		leaf := chain[len(chain)-1]
		b.Run("depth="+itoa(cfg.depth)+",bush="+itoa(cfg.bush), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := k.Isolated(leaf); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkEnabled 证明能力查询只随深度增长：固定深度时增加灌木节点耗时不变；
// 深度翻倍时耗时近线性增长（步长是每层一次来源匹配）。
func BenchmarkEnabled(b *testing.B) {
	for _, cfg := range []struct{ depth, bush int }{{64, 64}, {64, 4096}, {4096, 64}} {
		k, chain := buildDeepTree(b, cfg.depth, cfg.bush)
		leaf := chain[len(chain)-1]
		b.Run("depth="+itoa(cfg.depth)+",bush="+itoa(cfg.bush), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := k.Enabled(leaf, "camera"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
