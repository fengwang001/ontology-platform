package bcontext

import (
	"errors"
	"io"
	"math/rand"
	"testing"
)

// naiveNode 是朴素模型中的上下文：保留完整历史，不做任何缓存。
type naiveNode struct {
	id         int64
	parent     int64 // -1 表示顶层/弹窗
	docs       []naiveLoad
	allow      []map[string][]string // 每次装载时生效的嵌入允许列表快照
	fixedAllow map[string][]string
	opener     int64 // 弹窗开启者，-1 表示无
	dead       bool  // 父导航替换子树后置真，永不复活
}

type naiveLoad struct {
	doc      Document
	children []int64 // 该代文档下创建的子框架
}

// NaiveModel 每次查询都从完整历史重新推导，不与生产代码共享任何内部结构。
type NaiveModel struct {
	features map[string]Feature
	nodes    []*naiveNode
}

func NewNaiveModel(features map[string]Feature) *NaiveModel {
	clone := make(map[string]Feature, len(features))
	for name, f := range features {
		clone[name] = f
	}
	return &NaiveModel{features: clone}
}

func (m *NaiveModel) newNode(parent, opener int64) *naiveNode {
	n := &naiveNode{id: int64(len(m.nodes)), parent: parent, opener: opener, fixedAllow: map[string][]string{}}
	m.nodes = append(m.nodes, n)
	return n
}

func (m *NaiveModel) appendDoc(n *naiveNode, d Document) {
	n.docs = append(n.docs, naiveLoad{doc: d})
	n.allow = append(n.allow, cloneAllow(n.fixedAllow))
}

func (m *NaiveModel) load(parent *naiveNode, d Document, allow map[string][]string) *naiveNode {
	n := m.newNode(parent.id, -1)
	n.fixedAllow = cloneAllow(allow)
	m.appendDoc(n, d)
	gen := len(parent.docs) - 1
	parent.docs[gen].children = append(parent.docs[gen].children, n.id)
	return n
}

func (m *NaiveModel) kill(n *naiveNode) {
	n.dead = true
	gen := len(n.docs) - 1
	for _, cid := range n.docs[gen].children {
		m.kill(m.nodes[cid])
	}
}

func (m *NaiveModel) navigate(n *naiveNode, d Document) {
	gen := len(n.docs) - 1
	for _, cid := range n.docs[gen].children {
		m.kill(m.nodes[cid])
	}
	m.appendDoc(n, d)
}

func (m *NaiveModel) gen(n *naiveNode) *naiveLoad {
	if n.dead {
		return nil
	}
	return &n.docs[len(n.docs)-1]
}

func (m *NaiveModel) parentOf(n *naiveNode) *naiveNode {
	if n.parent == -1 {
		return nil
	}
	return m.nodes[n.parent]
}

// isolated 完全按装载时刻的父链状态重算。
func (m *NaiveModel) isolated(n *naiveNode) bool {
	chain := []*naiveNode{}
	for x := n; x != nil; x = m.parentOf(x) {
		chain = append(chain, x)
	}
	top := chain[len(chain)-1]
	topDoc := m.gen(top).doc
	if topDoc.Opener != OpenerSameOrigin || !topDoc.Embedder.strict() {
		return false
	}
	for i := len(chain) - 2; i >= 0; i-- {
		if !m.gen(chain[i]).doc.Embedder.strict() {
			return false
		}
	}
	return true
}

func (m *NaiveModel) embedOK(parent *naiveNode, d Document) bool {
	pd := m.gen(parent).doc
	if pd.Embedder.strict() && pd.Origin != d.Origin {
		return d.Embedder.strict()
	}
	return true
}

// enabled 自顶向下逐代逐条件重算。
func (m *NaiveModel) enabled(n *naiveNode, feature string) bool {
	feat := m.features[feature]
	chain := []*naiveNode{}
	for x := n; x != nil; x = m.parentOf(x) {
		chain = append(chain, x)
	}
	parentEnabled := true
	for i := len(chain) - 1; i >= 0; i-- {
		node := chain[i]
		g := m.gen(node)
		if feat.RequiresIsolation && !m.isolated(node) {
			return false
		}
		if !parentEnabled || !g.doc.declaredSelfAllowed(feature, feat.Default) {
			return false
		}
		if node.parent != -1 {
			parent := m.nodes[node.parent]
			pd := m.gen(parent).doc
			origins, declared := n_allowAt(m, node)[feature]
			allowed := false
			if declared {
				for _, o := range origins {
					if o == "*" || o == g.doc.Origin {
						allowed = true
					}
				}
			} else {
				allowed = feat.Default == DefaultAll || pd.Origin == g.doc.Origin
			}
			if !allowed {
				return false
			}
		}
		parentEnabled = true
	}
	return true
}

func n_allowAt(m *NaiveModel, n *naiveNode) map[string][]string {
	return n.allow[len(n.docs)-1]
}

// edgeRefs 按开窗时刻与之后每代导航逐次收紧（只断不恢复）。
func (m *NaiveModel) edgeRefs(opener, openee *naiveNode) (fwd, back bool) {
	fwd, back = true, true
	tighten := func(od, ed Document) {
		f, b := desiredOpenerRefs(od, ed)
		fwd = fwd && f
		back = back && b
	}
	tighten(opener.docs[0].doc, openee.docs[0].doc)
	for i := 1; i < len(opener.docs); i++ {
		tighten(opener.docs[i].doc, m.gen(openee).doc)
	}
	for i := 1; i < len(openee.docs); i++ {
		tighten(m.gen(opener).doc, openee.docs[i].doc)
	}
	return
}

type opKind int

const (
	opTop opKind = iota
	opFrame
	opNavigate
	opPopup
	opSetAllow
	opIsolated
	opEnabled
	opCrossRef
)

type op struct {
	kind    opKind
	target  int64
	target2 int64
	doc     Document
	allow   map[string][]string
	feature string
}

type opResult struct {
	ok  bool
	err error
}

var diffOrigins = []string{"https://a", "https://b", "https://c"}

func randomDoc(r *rand.Rand, forceValid bool) Document {
	origin := diffOrigins[r.Intn(len(diffOrigins))]
	d := Document{
		Origin:   origin,
		Opener:   OpenerPolicy(r.Intn(3)),
		Embedder: EmbedderPolicy(r.Intn(3)),
		Permissions: map[string][]string{
			"camera":   {origin},
			"geo":      {origin},
			"isolated": {origin},
		},
	}
	switch r.Intn(6) {
	case 0:
		d.Origin = ""
	case 1:
		d.Opener = OpenerPolicy(7)
	case 2:
		d.Embedder = EmbedderPolicy(9)
	case 3:
		d.Permissions[""] = []string{origin}
	case 4:
		d.Permissions = map[string][]string{"camera": {}} // 显式空声明：排除自身
	}
	if forceValid {
		d = Document{
			Origin:   origin,
			Opener:   OpenerPolicy(r.Intn(3)),
			Embedder: EmbedderPolicy(r.Intn(3)),
			Permissions: map[string][]string{
				"camera":   {origin},
				geoName(r): {origin},
			},
		}
	}
	return d
}

func geoName(r *rand.Rand) string {
	if r.Intn(2) == 0 {
		return "geo"
	}
	return "isolated"
}

func randomAllow(r *rand.Rand) map[string][]string {
	if r.Intn(3) == 0 {
		return nil
	}
	allow := map[string][]string{}
	for _, feat := range []string{"camera", "geo", "isolated"} {
		switch r.Intn(4) {
		case 0:
			allow[feat] = []string{"*"}
		case 1:
			allow[feat] = []string{diffOrigins[r.Intn(3)]}
		case 2:
			allow[feat] = []string{}
		}
	}
	return allow
}

func generateOps(r *rand.Rand, n int) []op {
	ops := []op{{kind: opTop, doc: randomDoc(r, true)}}
	alive := []int64{0}
	popups := []int64{}
	for i := 1; i < n; i++ {
		if len(alive) == 0 {
			ops = append(ops, op{kind: opTop, doc: randomDoc(r, true)})
			alive = append(alive, int64(i))
			continue
		}
		target := alive[r.Intn(len(alive))]
		switch r.Intn(12) {
		case 0:
			ops = append(ops, op{kind: opTop, doc: randomDoc(r, r.Intn(5) != 0)})
		case 1, 2, 3:
			ops = append(ops, op{kind: opFrame, target: target, doc: randomDoc(r, r.Intn(4) != 0), allow: randomAllow(r)})
		case 4, 5:
			ops = append(ops, op{kind: opNavigate, target: target, doc: randomDoc(r, r.Intn(4) != 0)})
		case 6:
			ops = append(ops, op{kind: opPopup, target: target, doc: randomDoc(r, r.Intn(5) != 0)})
			popups = append(popups, target)
		case 7:
			ops = append(ops, op{kind: opSetAllow, target: target, allow: randomAllow(r)})
		case 8:
			ops = append(ops, op{kind: opIsolated, target: target})
		case 9, 10:
			feat := []string{"camera", "geo", "isolated", "unknown-feature"}[r.Intn(4)]
			ops = append(ops, op{kind: opEnabled, target: target, feature: feat})
		default:
			if len(popups) > 0 {
				ops = append(ops, op{kind: opCrossRef, target: popups[r.Intn(len(popups))], target2: target})
			} else {
				i--
			}
		}
	}
	return ops
}

func replayKernel(t *testing.T, features map[string]Feature, ops []op) []opResult {
	t.Helper()
	k := NewKernel(features, NewLogger(io.Discard))
	out := make([]opResult, len(ops))
	for i, o := range ops {
		switch o.kind {
		case opTop:
			_, out[i].err = k.NewTopLevel(o.doc)
		case opFrame:
			_, out[i].err = k.LoadFrame(o.target, o.doc, o.allow)
		case opNavigate:
			out[i].err = k.Navigate(o.target, o.doc)
		case opPopup:
			_, out[i].err = k.OpenPopup(o.target, o.doc)
		case opSetAllow:
			out[i].err = k.SetFrameAllowlist(o.target, o.allow)
		case opIsolated:
			out[i].ok, out[i].err = k.Isolated(o.target)
		case opEnabled:
			out[i].ok, out[i].err = k.Enabled(o.target, o.feature)
		case opCrossRef:
			out[i].ok, out[i].err = k.CrossReference(o.target, o.target2)
		}
	}
	return out
}

func replayNaive(t *testing.T, features map[string]Feature, ops []op) []opResult {
	t.Helper()
	m := NewNaiveModel(features)
	out := make([]opResult, len(ops))
	node := func(id int64) *naiveNode {
		if id < 0 || int(id) >= len(m.nodes) {
			return nil
		}
		return m.nodes[id]
	}
	checkTarget := func(i int, id int64) *naiveNode {
		n := node(id)
		if n == nil {
			out[i].err = ErrContextNotExist
			return nil
		}
		if m.gen(n) == nil {
			out[i].err = ErrDocumentNotExist
			return nil
		}
		return n
	}
	for i, o := range ops {
		switch o.kind {
		case opTop:
			if err := o.doc.validate(); err != nil {
				out[i].err = err
				continue
			}
			n := m.newNode(-1, -1)
			m.appendDoc(n, o.doc)
		case opFrame:
			if err := o.doc.validate(); err != nil {
				out[i].err = err
				continue
			}
			if err := validateAllow(o.allow); err != nil {
				out[i].err = err
				continue
			}
			parent := checkTarget(i, o.target)
			if parent == nil {
				continue
			}
			if !m.embedOK(parent, o.doc) {
				out[i].err = ErrEmbedderMismatch
				continue
			}
		case opNavigate:
			if err := o.doc.validate(); err != nil {
				out[i].err = err
				continue
			}
			n := checkTarget(i, o.target)
			if n == nil {
				continue
			}
			if n.parent != -1 && !m.embedOK(m.nodes[n.parent], o.doc) {
				out[i].err = ErrEmbedderMismatch
				continue
			}
			m.navigate(n, o.doc)
		case opPopup:
			if err := o.doc.validate(); err != nil {
				out[i].err = err
				continue
			}
			opener := checkTarget(i, o.target)
			if opener == nil {
				continue
			}
			n := m.newNode(-1, opener.id)
			m.appendDoc(n, o.doc)
		case opSetAllow:
			if err := validateAllow(o.allow); err != nil {
				out[i].err = err
				continue
			}
			n := checkTarget(i, o.target)
			if n == nil {
				continue
			}
			n.fixedAllow = cloneAllow(o.allow)
		case opIsolated:
			n := checkTarget(i, o.target)
			if n != nil {
				out[i].ok = m.isolated(n)
			}
		case opEnabled:
			if o.feature == "" {
				out[i].err = ErrInvalidArgument
				continue
			}
			n := checkTarget(i, o.target)
			if n == nil {
				continue
			}
			if _, ok := m.features[o.feature]; !ok {
				out[i].err = ErrFeatureUnknown
				continue
			}
			out[i].ok = m.enabled(n, o.feature)
		case opCrossRef:
			from := node(o.target)
			to := node(o.target2)
			if from == nil || to == nil {
				out[i].err = ErrContextNotExist
				continue
			}
			if m.gen(from) == nil || m.gen(to) == nil {
				out[i].err = ErrDocumentNotExist
				continue
			}
			var opener, openee *naiveNode
			switch {
			case to.opener == from.id:
				opener, openee = from, to
			case from.opener == to.id:
				opener, openee = to, from
			default:
				out[i].ok = false
				continue
			}
			fwd, back := m.edgeRefs(opener, openee)
			allowed := fwd
			if opener.id != from.id {
				allowed = back
			}
			if !allowed {
				out[i].err = ErrOpenerGroupBroken
				continue
			}
			out[i].ok = true
		}
	}
	return out
}

var sentinels = []error{ErrInvalidArgument, ErrContextNotExist, ErrDocumentNotExist, ErrEmbedderMismatch, ErrOpenerGroupBroken, ErrFeatureUnknown}

func errClass(err error) error {
	for _, s := range sentinels {
		if errors.Is(err, s) {
			return s
		}
	}
	return nil
}

func TestNaiveDifferential(t *testing.T) {
	features := map[string]Feature{
		"camera":   {Default: DefaultSelf},
		"geo":      {Default: DefaultAll},
		"isolated": {RequiresIsolation: true, Default: DefaultAll},
	}
	for seed := int64(0); seed < 200; seed++ {
		r := rand.New(rand.NewSource(seed))
		ops := generateOps(r, 60)
		got := replayKernel(t, features, ops)
		want := replayNaive(t, features, ops)
		for i := range ops {
			if errClass(got[i].err) != errClass(want[i].err) || got[i].ok != want[i].ok {
				t.Fatalf("seed=%d op#%d kind=%d: kernel=(%v,%v) naive=(%v,%v)",
					seed, i, ops[i].kind, got[i].ok, got[i].err, want[i].ok, want[i].err)
			}
		}
	}
}

// TestNaiveDifferentialStress 使用更长序列覆盖深层导航与开窗重收紧。
func TestNaiveDifferentialStress(t *testing.T) {
	features := map[string]Feature{
		"camera":   {Default: DefaultSelf},
		"geo":      {Default: DefaultAll},
		"isolated": {RequiresIsolation: true, Default: DefaultAll},
	}
	for seed := int64(1000); seed < 1020; seed++ {
		r := rand.New(rand.NewSource(seed))
		ops := generateOps(r, 400)
		got := replayKernel(t, features, ops)
		want := replayNaive(t, features, ops)
		for i := range ops {
			if errClass(got[i].err) != errClass(want[i].err) || got[i].ok != want[i].ok {
				t.Fatalf("seed=%d op#%d kind=%d: kernel=(%v,%v) naive=(%v,%v)",
					seed, i, ops[i].kind, got[i].ok, got[i].err, want[i].ok, want[i].err)
			}
		}
	}
}
