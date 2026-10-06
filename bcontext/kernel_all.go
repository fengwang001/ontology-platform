package bcontext

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// parseHeader validates header fields and parses declarations. Feature names
// are *not* validated here: unknown-feature rejection happens after the
// embed-policy rejection stage.
func (k *Kernel) parseHeader(h Header) (*doc, error) {
	if h.Origin == "" {
		return nil, wrapErr(ErrInvalidArgument, "empty document origin")
	}
	switch h.Opener {
	case OpenerNone, OpenerSameOrigin, OpenerSameOriginAllowPopups:
	default:
		return nil, wrapErr(ErrInvalidArgument, "unknown opener policy %q", h.Opener)
	}
	switch h.Embedder {
	case EmbedderUnsafeNone, EmbedderRequireCorp, EmbedderCredentialless:
	default:
		return nil, wrapErr(ErrInvalidArgument, "unknown embedder policy %q", h.Embedder)
	}
	declared := make(map[string]*docDecl, len(h.Allow))
	for feature, origins := range h.Allow {
		if feature == "" {
			return nil, wrapErr(ErrInvalidArgument, "empty feature name")
		}
		d := &docDecl{origins: map[string]bool{}}
		for _, o := range origins {
			if o == "" {
				return nil, wrapErr(ErrInvalidArgument, "empty allowed origin for feature %q", feature)
			}
			if o == "*" {
				d.wildcard = true
				continue
			}
			d.origins[o] = true
		}
		declared[feature] = d
	}
	return &doc{
		origin:   h.Origin,
		opener:   h.Opener,
		embedder: h.Embedder,
		declared: declared,
	}, nil
}

func (k *Kernel) parseFrameAllow(fa FrameAllow) (*edge, error) {
	e := &edge{allow: map[string]*edgeAllow{}}
	for feature, origins := range fa.Allow {
		if feature == "" {
			return nil, wrapErr(ErrInvalidArgument, "empty feature name in frame allow")
		}
		a := &edgeAllow{origins: map[string]bool{}}
		for _, o := range origins {
			if o == "" {
				return nil, wrapErr(ErrInvalidArgument, "empty allowed origin for feature %q", feature)
			}
			if o == "*" {
				a.wildcard = true
				continue
			}
			a.origins[o] = true
		}
		e.allow[feature] = a
	}
	return e, nil
}

// validateKnownFeatures checks every feature referenced by a document
// declaration and its embedding edge against the kernel configuration.
func (k *Kernel) validateKnownFeatures(d *doc, e *edge) error {
	for f := range d.declared {
		if !k.cfg.featureKnown(f) {
			return wrapErr(ErrUnknownFeature, "document declares unknown feature %q", f)
		}
	}
	if e != nil {
		for f := range e.allow {
			if !k.cfg.featureKnown(f) {
				return wrapErr(ErrUnknownFeature, "frame allow lists unknown feature %q", f)
			}
		}
	}
	return nil
}

// embedAdmission enforces: a parent with a credential-requiring embedder
// policy may embed a cross-origin child only when that child is itself
// isolation-capable. Same-origin children are unrestricted.
func embedAdmission(parent, child *doc) bool {
	if parent.origin == child.origin {
		return true
	}
	if !isolationCapable(parent.embedder) {
		return true
	}
	return isolationCapable(child.embedder)
}

// LoadTop loads a new top-level browsing context.
func (k *Kernel) LoadTop(h Header) (int64, error) {
	var trace strings.Builder
	d, err := k.parseHeader(h)
	if err != nil {
		k.log.Log(k.fmtReject("LoadTop", h, nil, err))
		return 0, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()

	if err := k.validateKnownFeatures(d, nil); err != nil {
		k.log.Log(k.fmtReject("LoadTop", h, nil, err))
		return 0, err
	}
	d.isolated = d.opener == OpenerSameOrigin && isolationCapable(d.embedder)
	trace.WriteString("isolated=coop:same-origin AND coep:credentialed")

	id := k.allocID()
	k.contexts[id] = &context{id: id, doc: d}
	k.log.Log(k.fmtAccept("LoadTop", id, d, trace.String()))
	return id, nil
}

// LoadFrame loads a child document in a new frame.
func (k *Kernel) LoadFrame(parentID int64, h Header, fa FrameAllow) (int64, error) {
	d, err := k.parseHeader(h)
	if err != nil {
		k.log.Log(k.fmtReject("LoadFrame", h, &fa, err))
		return 0, err
	}
	ed, err := k.parseFrameAllow(fa)
	if err != nil {
		k.log.Log(k.fmtReject("LoadFrame", h, &fa, err))
		return 0, err
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	parent, ok := k.contexts[parentID]
	if !ok {
		err = wrapErr(ErrNoContext, "parent %d", parentID)
		k.log.Log(k.fmtReject("LoadFrame", h, &fa, err))
		return 0, err
	}
	if parent.doc == nil {
		err = wrapErr(ErrNoDocument, "parent %d has no document", parentID)
		k.log.Log(k.fmtReject("LoadFrame", h, &fa, err))
		return 0, err
	}
	if !embedAdmission(parent.doc, d) {
		err = wrapErr(ErrEmbedPolicy, "cross-origin child %q lacks credentialed embedder policy under %q",
			d.origin, parent.doc.origin)
		k.log.Log(k.fmtReject("LoadFrame", h, &fa, err))
		return 0, err
	}
	if err := k.validateKnownFeatures(d, ed); err != nil {
		k.log.Log(k.fmtReject("LoadFrame", h, &fa, err))
		return 0, err
	}

	d.isolated = parent.doc.isolated && isolationCapable(d.embedder)
	id := k.allocID()
	k.contexts[id] = &context{
		id:     id,
		doc:    d,
		parent: parent,
		edge:   ed,
	}
	k.log.Log(k.fmtAccept("LoadFrame", id, d,
		"isolated=parent.isolated AND coep:credentialed"))
	return id, nil
}

// Navigate replaces a context's document; a parent navigation tombstones the
// whole subtree. A non-nil fa replaces the frame allow list; nil keeps it.
func (k *Kernel) Navigate(id int64, h Header, fa *FrameAllow) error {
	d, err := k.parseHeader(h)
	if err != nil {
		k.log.Log(k.fmtRejectID("Navigate", id, h, fa, err))
		return err
	}
	var ed *edge
	if fa != nil {
		ed, err = k.parseFrameAllow(*fa)
		if err != nil {
			k.log.Log(k.fmtRejectID("Navigate", id, h, fa, err))
			return err
		}
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	ctx, ok := k.contexts[id]
	if !ok {
		err = wrapErr(ErrNoContext, "context %d", id)
		k.log.Log(k.fmtRejectID("Navigate", id, h, fa, err))
		return err
	}
	if ctx.doc == nil {
		err = wrapErr(ErrNoDocument, "context %d has no document", id)
		k.log.Log(k.fmtRejectID("Navigate", id, h, fa, err))
		return err
	}

	var admissionParent *doc
	var basis string
	if ctx.parent != nil {
		admissionParent = ctx.parent.doc
	}
	if admissionParent != nil && !embedAdmission(admissionParent, d) {
		err = wrapErr(ErrEmbedPolicy, "cross-origin navigation %q lacks credentialed embedder policy", d.origin)
		k.log.Log(k.fmtRejectID("Navigate", id, h, fa, err))
		return err
	}

	effectiveEdge := ctx.edge
	// The pending edge (from SetFrameAllow) wins; otherwise an edge supplied
	// directly with the navigation is fixed for this frame load.
	if ctx.parent != nil && ctx.pendingEdge != nil {
		effectiveEdge = ctx.pendingEdge
	} else if ed != nil && ctx.parent != nil {
		effectiveEdge = ed
	}
	if err := k.validateKnownFeatures(d, effectiveEdge); err != nil {
		k.log.Log(k.fmtRejectID("Navigate", id, h, fa, err))
		return err
	}

	if ctx.parent == nil {
		d.isolated = d.opener == OpenerSameOrigin && isolationCapable(d.embedder)
		basis = "root: coop:same-origin AND coep:credentialed"
	} else {
		d.isolated = ctx.parent.doc.isolated && isolationCapable(d.embedder)
		basis = "frame: parent.isolated AND coep:credentialed; parent chain unchanged"
	}
	ctx.doc = d
	if ctx.parent != nil && ctx.pendingEdge != nil {
		ctx.edge = ctx.pendingEdge
		ctx.pendingEdge = nil
	} else if ed != nil && ctx.parent != nil {
		ctx.edge = ed
	}

	// Parent-chain invariant: subtree contexts below this one keep the same
	// parent chain, but their documents are replaced by "no document".
	for _, other := range k.contexts {
		if other.parent != nil && isDescendant(ctx, other.parent) && other.doc != nil {
			other.doc = nil
			other.edge = nil
		}
	}

	k.log.Log(k.fmtAccept("Navigate", id, d, basis))
	return nil
}

func isDescendant(ancestor, c *context) bool {
	for cur := c; cur != nil; cur = cur.parent {
		if cur == ancestor {
			return true
		}
	}
	return false
}

// SetFrameAllow replaces the embedding allow list for future loads only.
func (k *Kernel) SetFrameAllow(id int64, fa FrameAllow) error {
	ed, err := k.parseFrameAllow(fa)
	if err != nil {
		k.log.Log(k.fmtRejectID("SetFrameAllow", id, Header{}, &fa, err))
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()

	ctx, ok := k.contexts[id]
	if !ok {
		err = wrapErr(ErrNoContext, "context %d", id)
		k.log.Log(k.fmtRejectID("SetFrameAllow", id, Header{}, &fa, err))
		return err
	}
	if ctx.doc == nil {
		err = wrapErr(ErrNoDocument, "context %d has no document", id)
		k.log.Log(k.fmtRejectID("SetFrameAllow", id, Header{}, &fa, err))
		return err
	}
	if ctx.parent == nil {
		err = wrapErr(ErrInvalidArgument, "context %d is not a frame", id)
		k.log.Log(k.fmtRejectID("SetFrameAllow", id, Header{}, &fa, err))
		return err
	}
	for f := range ed.allow {
		if !k.cfg.featureKnown(f) {
			err = wrapErr(ErrUnknownFeature, "frame allow lists unknown feature %q", f)
			k.log.Log(k.fmtRejectID("SetFrameAllow", id, Header{}, &fa, err))
			return err
		}
	}
	if err := k.validateKnownFeatures(ctx.doc, nil); err != nil {
		k.log.Log(k.fmtRejectID("SetFrameAllow", id, Header{}, &fa, err))
		return err
	}
	ctx.pendingEdge = ed
	k.log.Log(k.fmtAccept("SetFrameAllow", id, ctx.doc, "pending: future loads use new allow list; current document unaffected"))
	return nil
}

// OpenPopup opens a popup. Group relations are independent of the tree.
func (k *Kernel) OpenPopup(openerID int64, h Header) (int64, error) {
	d, err := k.parseHeader(h)
	if err != nil {
		k.log.Log(k.fmtRejectID("OpenPopup", openerID, h, nil, err))
		return 0, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()

	opener, ok := k.contexts[openerID]
	if !ok {
		err = wrapErr(ErrNoContext, "opener %d", openerID)
		k.log.Log(k.fmtRejectID("OpenPopup", openerID, h, nil, err))
		return 0, err
	}
	if opener.doc == nil {
		err = wrapErr(ErrNoDocument, "opener %d has no document", openerID)
		k.log.Log(k.fmtRejectID("OpenPopup", openerID, h, nil, err))
		return 0, err
	}
	if err := k.validateKnownFeatures(d, nil); err != nil {
		k.log.Log(k.fmtRejectID("OpenPopup", openerID, h, nil, err))
		return 0, err
	}

	// A popup is an independent root for isolation evaluation.
	d.isolated = d.opener == OpenerSameOrigin && isolationCapable(d.embedder)

	sameOrigin := opener.doc.origin == d.origin
	broken := false
	basis := "group retained"
	switch {
	case d.opener == OpenerSameOrigin && !sameOrigin:
		broken = true
		basis = "broken: openee coop:same-origin with cross-origin opener"
	case opener.doc.opener == OpenerSameOrigin && !sameOrigin:
		broken = true
		basis = "broken: opener coop:same-origin and cross-origin openee"
	default:
		// same-origin-allow-popups keeps the group, but an openee that itself
		// declares same-origin severs a cross-origin opener (handled above);
		// same-origin openees remain grouped.
	}

	id := k.allocID()
	k.contexts[id] = &context{
		id:           id,
		doc:          d,
		popupOf:      openerID,
		openerBroken: broken,
	}
	k.log.Log(k.fmtAccept("OpenPopup", id, d, basis))
	return id, nil
}

// Isolated reports the isolation state fixed for the context's current
// document. The lookup is one map read plus one field read: O(1).
func (k *Kernel) Isolated(id int64) (bool, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()

	ctx, ok := k.contexts[id]
	if !ok {
		err := wrapErr(ErrNoContext, "context %d", id)
		k.log.Log("Isolated id=" + itoa(id) + " -> " + err.Error())
		return false, err
	}
	if ctx.doc == nil {
		err := wrapErr(ErrNoDocument, "context %d", id)
		k.log.Log("Isolated id=" + itoa(id) + " -> " + err.Error())
		return false, err
	}
	k.log.Log("Isolated id=" + itoa(id) + " origin=" + ctx.doc.origin +
		" -> " + boolStr(ctx.doc.isolated) +
		" [cached at load: " + isolationBasis(ctx) + "]")
	return ctx.doc.isolated, nil
}

func isolationBasis(c *context) string {
	if c.parent == nil {
		return "root isolated iff coop=same-origin and coep credentialed"
	}
	return "frame isolated iff parent.isolated and coep credentialed"
}

// FeatureAllowed evaluates one feature for the context's current document.
// It walks the document-to-root chain exactly once, so the cost is O(depth)
// and independent of the total number of contexts.
func (k *Kernel) FeatureAllowed(id int64, feature string) (bool, error) {
	if feature == "" {
		err := wrapErr(ErrInvalidArgument, "empty feature name")
		k.log.Log("FeatureAllowed id=" + itoa(id) + " feature=\"\" -> " + err.Error())
		return false, err
	}

	k.mu.RLock()
	defer k.mu.RUnlock()

	ctx, ok := k.contexts[id]
	if !ok {
		err := wrapErr(ErrNoContext, "context %d", id)
		k.log.Log("FeatureAllowed id=" + itoa(id) + " feature=" + feature + " -> " + err.Error())
		return false, err
	}
	if ctx.doc == nil {
		err := wrapErr(ErrNoDocument, "context %d", id)
		k.log.Log("FeatureAllowed id=" + itoa(id) + " feature=" + feature + " -> " + err.Error())
		return false, err
	}
	if !k.cfg.featureKnown(feature) {
		err := wrapErr(ErrUnknownFeature, "feature %q", feature)
		k.log.Log("FeatureAllowed id=" + itoa(id) + " feature=" + feature + " -> " + err.Error())
		return false, err
	}

	var trace strings.Builder
	trace.WriteString("chain child->root: ")

	allowed := true
	cur := ctx
	for cur != nil {
		// Isolation gate: isolation is a stored flag, so it costs O(1) per hop.
		if k.cfg.requiresIsolation(feature) && !cur.doc.isolated {
			allowed = false
			trace.WriteString(originTag(cur) + "[isolation-required but not isolated]; ")
			break
		}
		if cur.parent == nil {
			if !cur.doc.allowsOrigin(feature, cur.doc.origin, k.cfg) {
				allowed = false
				trace.WriteString(originTag(cur) + "[top policy excludes self/default]; ")
			} else {
				trace.WriteString(originTag(cur) + "[top allows]; ")
			}
			cur = cur.parent
			continue
		}
		if !cur.doc.allowsOwnOrigin(feature, k.cfg) {
			allowed = false
			trace.WriteString(originTag(cur) + "[self declaration excludes own origin]; ")
			break
		}
		if !cur.edge.allows(feature, cur.doc.origin, cur.parent.doc.origin, k.cfg) {
			allowed = false
			trace.WriteString(originTag(cur) + "[parent frame allow list denies]; ")
			break
		}
		trace.WriteString(originTag(cur) + "[self+edge ok]; ")
		cur = cur.parent
	}

	k.log.Log("FeatureAllowed id=" + itoa(id) + " origin=" + ctx.doc.origin +
		" feature=" + feature + " -> " + boolStr(allowed) + " [" + trace.String() + "]")
	return allowed, nil
}

// OpenerReference reports the opener of a popup. If the opener group was
// severed at open time, it returns ErrOpenerBroken; the severance is one-way
// and permanent.
func (k *Kernel) OpenerReference(child int64) (int64, bool, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()

	ctx, ok := k.contexts[child]
	if !ok {
		err := wrapErr(ErrNoContext, "context %d", child)
		k.log.Log("OpenerReference id=" + itoa(child) + " -> " + err.Error())
		return 0, false, err
	}
	if ctx.doc == nil {
		err := wrapErr(ErrNoDocument, "context %d", child)
		k.log.Log("OpenerReference id=" + itoa(child) + " -> " + err.Error())
		return 0, false, err
	}
	if ctx.popupOf == 0 {
		k.log.Log("OpenerReference id=" + itoa(child) + " -> no opener")
		return 0, false, nil
	}
	if ctx.openerBroken {
		err := wrapErr(ErrOpenerBroken, "popup %d cannot reference opener %d", child, ctx.popupOf)
		k.log.Log("OpenerReference id=" + itoa(child) + " -> " + err.Error())
		return 0, false, err
	}
	k.log.Log("OpenerReference id=" + itoa(child) + " -> opener=" + itoa(ctx.popupOf) + " [group retained]")
	return ctx.popupOf, true, nil
}

func originTag(c *context) string {
	if c.parent == nil {
		return "root(" + c.doc.origin + ")"
	}
	return "frame(" + c.doc.origin + ")"
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func headerStr(h Header) string {
	if h.Origin == "" {
		return "{}"
	}
	return fmt.Sprintf("{origin=%s coop=%s coep=%s allow=%v}", h.Origin, h.Opener, h.Embedder, h.Allow)
}

func frameStr(fa *FrameAllow) string {
	if fa == nil {
		return "<unchanged>"
	}
	return fmt.Sprintf("allow=%v", fa.Allow)
}

func (k *Kernel) fmtReject(op string, h Header, fa *FrameAllow, err error) string {
	return op + " input=" + headerStr(h) + " frame=" + frameStr(fa) + " -> REJECT: " + err.Error()
}

func (k *Kernel) fmtRejectID(op string, id int64, h Header, fa *FrameAllow, err error) string {
	return fmt.Sprintf("%s id=%d input=%s frame=%s -> REJECT: %s", op, id, headerStr(h), frameStr(fa), err.Error())
}

func (k *Kernel) fmtAccept(op string, id int64, d *doc, basis string) string {
	return fmt.Sprintf("%s id=%d origin=%s coop=%s coep=%s isolated=%t -> ACCEPT [%s]",
		op, id, d.origin, d.opener, d.embedder, d.isolated, basis)
}

// Kernel is a concurrently usable browsing-context tree.
type Kernel struct {
	mu     sync.RWMutex
	nextID atomic.Int64

	cfg Config
	log Logger

	contexts map[int64]*context
}

// New creates an empty kernel with the given feature configuration.
func New(cfg Config) *Kernel {
	return &Kernel{
		cfg:      cfg,
		log:      nopLogger{},
		contexts: map[int64]*context{},
	}
}

// WithLogger attaches an operation logger.
func (k *Kernel) WithLogger(l Logger) *Kernel {
	if l != nil {
		k.log = l
	}
	return k
}

func (k *Kernel) allocID() int64 { return k.nextID.Add(1) }

func wrapErr(cause error, format string, args ...any) error {
	return fmt.Errorf("%w: %s", cause, fmt.Sprintf(format, args...))
}
