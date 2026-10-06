package bcontext

import (
	"fmt"
	"maps"
	"sync"
)

// Kernel 是浏览上下文隔离与权限策略求值内核，并发调用等价于某一串行顺序。
type Kernel struct {
	mu       sync.RWMutex
	features map[string]Feature
	contexts map[int64]*contextNode
	// edges 以“被开启者 id”为键：一个被开启者至多有一条入边；断开永久保留记录。
	edges       map[int64]*openerEdge
	openerEdges map[int64][]*openerEdge
	nextID      int64
	log         *Logger
}

// NewKernel 创建内核并注册能力。feature 名为空视为参数非法。
func NewKernel(features map[string]Feature, logger *Logger) *Kernel {
	if logger == nil {
		logger = NewLogger(nil)
	}
	return &Kernel{
		features:    maps.Clone(features),
		contexts:    make(map[int64]*contextNode),
		edges:       make(map[int64]*openerEdge),
		openerEdges: make(map[int64][]*openerEdge),
		log:         logger,
	}
}

func validateAllow(allow map[string][]string) error {
	for feature, origins := range allow {
		if feature == "" {
			return fmt.Errorf("%w: empty feature name in embedding allowlist", ErrInvalidArgument)
		}
		for _, origin := range origins {
			if origin == "" {
				return fmt.Errorf("%w: empty origin in embedding allowlist", ErrInvalidArgument)
			}
		}
	}
	return nil
}

func cloneAllow(allow map[string][]string) map[string][]string {
	if len(allow) == 0 {
		return map[string][]string{}
	}
	out := make(map[string][]string, len(allow))
	for feature, origins := range allow {
		out[feature] = append([]string(nil), origins...)
	}
	return out
}

// lookup 在持锁状态下按拒绝次序解析上下文与其当前文档。
func (k *Kernel) lookup(ctxID int64) (*contextNode, error) {
	node, ok := k.contexts[ctxID]
	if !ok {
		return nil, ErrContextNotExist
	}
	if !node.alive() {
		return nil, ErrDocumentNotExist
	}
	return node, nil
}

// installDocument 在已存在上下文上装载文档：固定隔离状态与嵌入允许列表快照。
func (c *contextNode) installDocument(doc Document, isolated bool) {
	c.doc = &doc
	c.isolated = isolated
	if c.parent != nil {
		c.allowAtLoad = cloneAllow(c.allow)
	}
}

// recheckEdgesFor 在文档导航后重新收紧涉及 node 的开启者组边（只断不恢复）。
func (k *Kernel) recheckEdgesFor(node *contextNode) {
	if edge, ok := k.edges[node.id]; ok {
		if edge.reevaluate() {
			k.log.logf("opener group edge opener=%d openee=%d re-severed after navigation", edge.opener.id, edge.openee.id)
		}
	}
	for _, edge := range k.openerEdges[node.id] {
		if edge.reevaluate() {
			k.log.logf("opener group edge opener=%d openee=%d re-severed after navigation", edge.opener.id, edge.openee.id)
		}
	}
}

// NewTopLevel 在新顶层浏览上下文中装载文档。
func (k *Kernel) NewTopLevel(doc Document) (int64, error) {
	if err := doc.validate(); err != nil {
		k.log.logf("NewTopLevel rejected: %v (input origin=%q opener=%s embedder=%s)", err, doc.Origin, doc.Opener, doc.Embedder)
		return 0, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	id := k.nextID
	k.nextID++
	node := &contextNode{id: id, allow: map[string][]string{}}
	k.contexts[id] = node
	node.top = node
	isolated := computeIsolated(nil, doc)
	node.installDocument(doc, isolated)
	k.log.logf("NewTopLevel ok id=%d origin=%q isolated=%t (opener=%s embedder=%s)", id, doc.Origin, isolated, doc.Opener, doc.Embedder)
	return id, nil
}

// LoadFrame 在 parentID 下创建嵌入框架并装载文档，allow 为嵌入属性允许列表。
func (k *Kernel) LoadFrame(parentID int64, doc Document, allow map[string][]string) (int64, error) {
	if err := doc.validate(); err != nil {
		k.log.logf("LoadFrame rejected: %v", err)
		return 0, err
	}
	if err := validateAllow(allow); err != nil {
		k.log.logf("LoadFrame rejected: %v (embedding allowlist)", err)
		return 0, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	parent, err := k.lookup(parentID)
	if err != nil {
		k.log.logf("LoadFrame rejected: %v parent=%d", err, parentID)
		return 0, err
	}
	if !embedAdmissionOK(parent, doc) {
		k.log.logf("LoadFrame rejected: %v parent=%q strict=%t child=%q embedder=%s cross-origin",
			ErrEmbedderMismatch, parent.doc.Origin, parent.doc.Embedder.strict(), doc.Origin, doc.Embedder)
		return 0, ErrEmbedderMismatch
	}
	id := k.nextID
	k.nextID++
	node := &contextNode{
		id:     id,
		parent: parent,
		top:    parent.top,
		allow:  cloneAllow(allow),
	}
	parent.children = append(parent.children, node)
	k.contexts[id] = node
	isolated := computeIsolated(parent, doc)
	node.installDocument(doc, isolated)
	k.log.logf("LoadFrame ok id=%d parent=%d origin=%q isolated=%t (topIsolated=%t childEmbedder=%s)",
		id, parentID, doc.Origin, isolated, parent.top.isolated, doc.Embedder)
	return id, nil
}

// Navigate 将上下文导航到新文档：父链与（框架的）嵌入允许列表不变，
// 隔离状态与权限策略按新文档头重推导；顶层文档导航时其整棵子树被替换。
func (k *Kernel) Navigate(ctxID int64, doc Document) error {
	if err := doc.validate(); err != nil {
		k.log.logf("Navigate rejected: %v", err)
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	node, err := k.lookup(ctxID)
	if err != nil {
		k.log.logf("Navigate rejected: %v ctx=%d", err, ctxID)
		return err
	}
	if node.parent != nil && !embedAdmissionOK(node.parent, doc) {
		k.log.logf("Navigate rejected: %v ctx=%d", ErrEmbedderMismatch, ctxID)
		return ErrEmbedderMismatch
	}
	for _, child := range node.children {
		markDetached(child)
	}
	node.children = nil
	isolated := computeIsolated(node.parent, doc)
	node.installDocument(doc, isolated)
	k.log.logf("Navigate ok ctx=%d origin=%q isolated=%t; subtree replaced (%d descendant document(s) now absent)",
		ctxID, doc.Origin, isolated, len(node.children))
	k.recheckEdgesFor(node)
	return nil
}

// OpenPopup 由 openerID 开启弹窗装载 doc，返回新顶层上下文 id。
func (k *Kernel) OpenPopup(openerID int64, doc Document) (int64, error) {
	if err := doc.validate(); err != nil {
		k.log.logf("OpenPopup rejected: %v", err)
		return 0, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	opener, err := k.lookup(openerID)
	if err != nil {
		k.log.logf("OpenPopup rejected: %v opener=%d", err, openerID)
		return 0, err
	}
	id := k.nextID
	k.nextID++
	node := &contextNode{id: id, allow: map[string][]string{}, openedFrom: opener}
	k.contexts[id] = node
	node.top = node
	isolated := computeIsolated(nil, doc)
	node.installDocument(doc, isolated)
	fwd, back := desiredOpenerRefs(*opener.doc, doc)
	edge := &openerEdge{opener: opener, openee: node, openerToOpenee: fwd, openeeToOpener: back}
	k.edges[id] = edge
	k.openerEdges[opener.id] = append(k.openerEdges[opener.id], edge)
	k.log.logf("OpenPopup ok id=%d opener=%d origin=%q isolated=%t groupRefs(open->opened=%t, opened->open=%t)",
		id, openerID, doc.Origin, isolated, fwd, back)
	return id, nil
}

// SetFrameAllowlist 修改框架嵌入属性允许列表，只对此后装载的文档生效。
func (k *Kernel) SetFrameAllowlist(ctxID int64, allow map[string][]string) error {
	if err := validateAllow(allow); err != nil {
		k.log.logf("SetFrameAllowlist rejected: %v", err)
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	node, err := k.lookup(ctxID)
	if err != nil {
		k.log.logf("SetFrameAllowlist rejected: %v ctx=%d", err, ctxID)
		return err
	}
	node.allow = cloneAllow(allow)
	k.log.logf("SetFrameAllowlist ok ctx=%d; current document keeps load-time snapshot, future loads use the new allowlist", ctxID)
	return nil
}

// Isolated 查询文档装载时确定的跨源隔离状态，开销与深度无关。
func (k *Kernel) Isolated(ctxID int64) (bool, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	node, err := k.lookup(ctxID)
	if err != nil {
		return false, err
	}
	k.log.logf("Isolated ctx=%d => %t (cached at load time, O(1))", ctxID, node.isolated)
	return node.isolated, nil
}

// Enabled 查询能力在文档中是否可用，开销仅与该文档到顶层的深度有关。
func (k *Kernel) Enabled(ctxID int64, feature string) (bool, error) {
	if feature == "" {
		k.log.logf("Enabled rejected: %v (empty feature name)", ErrInvalidArgument)
		return false, ErrInvalidArgument
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	node, err := k.lookup(ctxID)
	if err != nil {
		k.log.logf("Enabled rejected: %v ctx=%d feature=%q", err, ctxID, feature)
		return false, err
	}
	if _, known := k.features[feature]; !known {
		k.log.logf("Enabled rejected: %v feature=%q", ErrFeatureUnknown, feature)
		return false, ErrFeatureUnknown
	}
	enabled, reason := k.featureEnabled(node, feature)
	k.log.logf("Enabled ctx=%d feature=%q => %t (%s)", ctxID, feature, enabled, reason)
	return enabled, nil
}

// CrossReference 判定 fromID 是否可跨引用 toID：仅同组且该方向未断开可达。
func (k *Kernel) CrossReference(fromID, toID int64) (bool, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	from, ok := k.contexts[fromID]
	if !ok {
		return false, ErrContextNotExist
	}
	to, ok := k.contexts[toID]
	if !ok {
		return false, ErrContextNotExist
	}
	if !from.alive() || !to.alive() {
		return false, ErrDocumentNotExist
	}
	var edge *openerEdge
	allowed := false
	if e, ok := k.edges[toID]; ok && e.opener.id == fromID {
		edge = e
		allowed = e.openerToOpenee // from=opener -> to=openee
	} else if e, ok := k.edges[fromID]; ok && e.opener.id == toID {
		edge = e
		allowed = e.openeeToOpener // from=openee -> to=opener
	}
	if edge == nil {
		k.log.logf("CrossReference %d -> %d => false (never shared an opener group)", fromID, toID)
		return false, nil
	}
	if !allowed {
		k.log.logf("CrossReference %d -> %d rejected: %v", fromID, toID, ErrOpenerGroupBroken)
		return false, ErrOpenerGroupBroken
	}
	k.log.logf("CrossReference %d -> %d => true (group edge intact in this direction)", fromID, toID)
	return true, nil
}
