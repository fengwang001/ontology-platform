package growstack

// Value is a slot value: either a plain integer or an intra-stack pointer.
type Value struct {
	plain int64
	isPtr bool
	ptr   *stackPointer
}

// stackPointer is the runtime form of an intra-stack pointer.
type stackPointer struct {
	srcCO    int64 // owning coroutine id of the cell holding this value
	targetCO int64 // target coroutine id (equals srcCO when accepted)
	frame    int64 // target frame id
	slot     int   // target slot index within the frame
	dead     bool  // set when the target frame is popped
	srcFrame int64
	srcSlot  int
}

// coroutine owns one relocatable stack and its pointer-tracking indexes.
type coroutine struct {
	id         int64
	size       int
	used       int
	hw         int
	grows      int
	shr        int
	base       []cell
	frameSlots int
	frames     []frameInfo
	nextFrame  int64
	// ptrs holds the stackPointer contained in every live cell of this
	// coroutine, keyed by (frame id, slot). A cell without a pointer is not
	// present, so relocation fixup work is O(number of intra-stack pointers),
	// not O(stack size).
	ptrs map[cellKey]*stackPointer
	// inbound indexes, for each target cell, the pointers pointing at it.
	inbound map[cellKey]map[*stackPointer]struct{}
}

type frameInfo struct {
	id    int64
	start int
}

type cellKey struct {
	frame int64
	slot  int
}

// cell is one physical slot in the backing array.
type cell struct {
	kind byte
	ival int64
	pval *stackPointer
}

const (
	cellEmpty byte = iota
	cellInt
	cellPtr
)

// Handle is the opaque, non-escapable pointer token returned to callers.
type Handle struct{ sp *stackPointer }

// Target reports the logical (coroutine, frame, slot) target, for tests.
func (h Handle) Target() (int64, int64, int) {
	if h.sp == nil {
		return 0, 0, 0
	}
	return h.sp.targetCO, h.sp.frame, h.sp.slot
}

func targetStr(h Handle) string {
	co, f, s := h.Target()
	return "(" + itoa(co) + "," + itoa(f) + "," + itoa64(int64(s)) + ")"
}

// frameOffset maps a cellKey's frame to its physical start.
func (k cellKey) frameOffset(c *coroutine) int {
	if _, fi, ok := c.frameIndex(k.frame); ok {
		return fi.start
	}
	return 0
}

func (cl cell) value() Value {
	if cl.kind == cellPtr {
		return Value{isPtr: true, ptr: cl.pval}
	}
	return Value{plain: cl.ival}
}

// IsInt reports whether the value read is a plain integer.
func (v Value) IsInt() bool { return !v.isPtr }

// Int returns the plain integer carried by the value.
func (v Value) Int() int64 { return v.plain }

// IsPointer reports whether the value is an intra-stack pointer.
func (v Value) IsPointer() bool { return v.isPtr }

type logVal Value

func logValue(v Value) logVal { return logVal(v) }

func (v logVal) String() string {
	if v.isPtr {
		if v.ptr == nil {
			return "ptr<nil>"
		}
		return "ptr(target=" + itoa(v.ptr.targetCO) + "," + itoa(v.ptr.frame) + "," + itoa64(int64(v.ptr.slot)) + ",dead=" + boolStr(v.ptr.dead) + ")"
	}
	return "int(" + itoa64(v.plain) + ")"
}

func itoa(i int64) string { return itoa64(i) }
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func itoa64(i int64) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [24]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		buf[n] = '-'
	}
	return string(buf[n:])
}

func (r *Runtime) failf(op string, err error, in ...any) error {
	r.log.Logf("op=%s in=%v -> REJECT kind=%s reason=%q", op, in, kindOf(err), err.Error())
	return err
}

func (r *Runtime) okf(op string, out any, in ...any) {
	r.log.Logf("op=%s in=%v -> ACCEPT out=%v reason=preconditions-satisfied", op, in, out)
}

func (r *Runtime) lookup(coID int64) (*coroutine, error) {
	c, ok := r.stacks[coID]
	if !ok {
		return nil, &Error{Kind: ErrUndefined, Msg: "coroutine not defined"}
	}
	return c, nil
}

func (r *Runtime) cellAt(c *coroutine, frameID int64, slot int) (*cell, error) {
	_, fi, ok := c.frameIndex(frameID)
	if !ok {
		return nil, &Error{Kind: ErrUndefined, Msg: "frame not defined"}
	}
	if slot < 0 || slot >= c.frameSlots {
		return nil, &Error{Kind: ErrArgument, Msg: "slot index out of range"}
	}
	return &c.base[fi.start+slot], nil
}

// NewCoroutine registers a coroutine with one base-sized stack. The base
// allocation is charged against the global quota immediately.
func (r *Runtime) NewCoroutine(id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.stacks[id]; exists {
		return r.failf("new-co", &Error{Kind: ErrArgument, Msg: "duplicate coroutine id"}, id)
	}
	if r.cfg.BaseSize > r.remaining {
		return r.failf("new-co", &Error{Kind: ErrQuota, Msg: "base stack exceeds global quota"}, id)
	}
	r.allocAttempts++
	base, ok := r.alloc.Alloc(r.cfg.BaseSize)
	if !ok {
		return r.failf("new-co", &Error{Kind: ErrQuota, Msg: "base allocation rejected"}, id)
	}
	r.remaining -= r.cfg.BaseSize
	c := &coroutine{
		id:         id,
		size:       r.cfg.BaseSize,
		base:       base,
		frameSlots: r.cfg.FrameSlots,
		ptrs:       map[cellKey]*stackPointer{},
		inbound:    map[cellKey]map[*stackPointer]struct{}{},
	}
	c.hw = c.size
	r.stacks[id] = c
	r.okf("new-co", c.size, id)
	return nil
}

// Push appends a frame, growing first when necessary.
func (r *Runtime) Push(coID int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, err := r.lookup(coID)
	if err != nil {
		return 0, r.failf("push", err, coID)
	}
	need := c.used + c.frameSlots
	if need > c.size {
		target := r.nextSize(c.size, need)
		if target == 0 {
			return 0, r.failf("push", &Error{Kind: ErrOverflow, Msg: "per-coroutine stack limit reached"}, coID)
		}
		delta := target - c.size
		if delta > r.remaining {
			return 0, r.failf("push", &Error{Kind: ErrQuota, Msg: "global quota insufficient for growth"}, coID)
		}
		oldSize := c.size
		if !r.relocate(c, target) {
			return 0, r.failf("push", &Error{Kind: ErrQuota, Msg: "relocation allocation rejected; old stack intact"}, coID)
		}
		c.size = target
		c.grows++
		c.hw = target
		r.remaining -= delta
		r.log.Logf("op=relocate-grow co=%d old-size=%d new-size=%d pointers-fixed=%d cells-copied=%d",
			coID, oldSize, target, r.lastRelocFixed, r.lastRelocCopied)
	}
	fid := c.nextFrame
	c.nextFrame++
	c.frames = append(c.frames, frameInfo{id: fid, start: c.used})
	c.used += c.frameSlots
	r.okf("push", fid, coID)
	return fid, nil
}

// Pop removes the top frame, marks inbound pointers dangling, and shrinks
// the backing stack automatically when utilization crosses the threshold.
func (r *Runtime) Pop(coID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, err := r.lookup(coID)
	if err != nil {
		return r.failf("pop", err, coID)
	}
	if len(c.frames) == 0 {
		return r.failf("pop", &Error{Kind: ErrArgument, Msg: "no frame to pop"}, coID)
	}
	top := c.frames[len(c.frames)-1]
	oldSize := c.size
	c.markFrameDangling(top.id)
	c.dropFrameCells(top.id)
	c.frames = c.frames[:len(c.frames)-1]
	c.used -= c.frameSlots

	if target := r.shrinkSize(c.used); target > 0 {
		if r.relocate(c, target) {
			delta := c.size - target
			c.size = target
			c.shr++
			r.remaining += delta
			r.log.Logf("op=relocate-shrink co=%d old-size=%d new-size=%d pointers-fixed=%d cells-copied=%d",
				coID, oldSize, target, r.lastRelocFixed, r.lastRelocCopied)
		} else {
			r.log.Logf("op=pop co=%d shrink-allocation-rejected keeping-size=%d (pop retained)", coID, c.size)
		}
	}
	r.okf("pop", c.used, coID)
	return nil
}

// Store writes a plain integer into an existing slot.
func (r *Runtime) Store(coID int64, frame int64, slot int, v int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, err := r.lookup(coID)
	if err != nil {
		return r.failf("store", err, coID, frame, slot, v)
	}
	cl, err := r.cellAt(c, frame, slot)
	if err != nil {
		return r.failf("store", err, coID, frame, slot, v)
	}
	c.dropCellPointer(cellKey{frame, slot})
	*cl = cell{kind: cellInt, ival: v}
	r.okf("store", v, coID, frame, slot)
	return nil
}

// Load reads a slot. A returned value may be a dangling pointer; dereference
// it through ReadPtr to observe the dangling rejection.
func (r *Runtime) Load(coID int64, frame int64, slot int) (Value, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, err := r.lookup(coID)
	if err != nil {
		return Value{}, r.failf("load", err, coID, frame, slot)
	}
	cl, err := r.cellAt(c, frame, slot)
	if err != nil {
		return Value{}, r.failf("load", err, coID, frame, slot)
	}
	v := cl.value()
	r.okf("load", logValue(v), coID, frame, slot)
	return v, nil
}

// MkPtr mints a pointer to (dstCO,dstFrame,dstSlot). Nothing is stored, so
// the token is harmless until it is copied or published.
func (r *Runtime) MkPtr(srcCO int64, srcFrame int64, srcSlot int, dstCO int64, dstFrame int64, dstSlot int) (Handle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	in := []any{srcCO, srcFrame, srcSlot, dstCO, dstFrame, dstSlot}
	sc, err := r.lookup(srcCO)
	if err != nil {
		return Handle{}, r.failf("mkptr", err, in)
	}
	dc, err := r.lookup(dstCO)
	if err != nil {
		return Handle{}, r.failf("mkptr", err, in)
	}
	if _, _, ok := sc.frameIndex(srcFrame); !ok {
		return Handle{}, r.failf("mkptr", &Error{Kind: ErrUndefined, Msg: "source frame not defined"}, in)
	}
	if _, _, ok := dc.frameIndex(dstFrame); !ok {
		return Handle{}, r.failf("mkptr", &Error{Kind: ErrUndefined, Msg: "target frame not defined"}, in)
	}
	if srcSlot < 0 || srcSlot >= sc.frameSlots || dstSlot < 0 || dstSlot >= dc.frameSlots {
		return Handle{}, r.failf("mkptr", &Error{Kind: ErrArgument, Msg: "slot index out of range"}, in)
	}
	sp := &stackPointer{
		srcCO:    srcCO,
		targetCO: dstCO,
		frame:    dstFrame,
		slot:     dstSlot,
		srcFrame: srcFrame,
		srcSlot:  srcSlot,
	}
	r.okf("mkptr", Handle{sp}, in)
	return Handle{sp}, nil
}

// CopyPtr writes a pointer into a slot of the same stack. Cross-stack copies
// are rejected at write time, before any state changes.
func (r *Runtime) CopyPtr(coID int64, frame int64, slot int, h Handle) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	in := []any{coID, frame, slot, targetStr(h)}
	c, err := r.lookup(coID)
	if err != nil {
		return r.failf("copyptr", err, in)
	}
	if h.sp == nil {
		return r.failf("copyptr", &Error{Kind: ErrArgument, Msg: "nil handle"}, in)
	}
	if _, err := r.cellAt(c, frame, slot); err != nil {
		return r.failf("copyptr", err, in)
	}
	if h.sp.dead {
		return r.failf("copyptr", &Error{Kind: ErrDangling, Msg: "source pointer is dangling"}, in)
	}
	if h.sp.targetCO != coID {
		return r.failf("copyptr", &Error{Kind: ErrCrossStack, Msg: "pointer targets another coroutine stack"}, in)
	}
	if _, _, ok := c.frameIndex(h.sp.frame); !ok {
		return r.failf("copyptr", &Error{Kind: ErrDangling, Msg: "target frame gone"}, in)
	}
	cl, _ := r.cellAt(c, frame, slot)
	c.dropCellPointer(cellKey{frame, slot})
	sp := &stackPointer{
		srcCO: coID, targetCO: coID,
		frame: h.sp.frame, slot: h.sp.slot,
		srcFrame: frame, srcSlot: slot,
	}
	*cl = cell{kind: cellPtr, pval: sp}
	c.registerPointer(sp)
	r.okf("copyptr", targetStr(h), in)
	return nil
}

func (r *Runtime) resolve(h Handle) (*coroutine, *cell, error) {
	if h.sp == nil {
		return nil, nil, &Error{Kind: ErrArgument, Msg: "nil handle"}
	}
	c, ok := r.stacks[h.sp.targetCO]
	if !ok {
		return nil, nil, &Error{Kind: ErrUndefined, Msg: "target coroutine not defined"}
	}
	cl, ok := c.targetCell(h.sp)
	if !ok {
		return nil, nil, &Error{Kind: ErrDangling, Msg: "pointer targets a popped frame"}
	}
	return c, cl, nil
}

// ReadPtr dereferences a pointer and reads its target slot.
func (r *Runtime) ReadPtr(h Handle) (Value, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, cl, err := r.resolve(h)
	if err != nil {
		return Value{}, r.failf("readptr", err, targetStr(h))
	}
	v := cl.value()
	r.okf("readptr", logValue(v), targetStr(h), c.id)
	return v, nil
}

// WritePtr writes a plain integer through a pointer.
func (r *Runtime) WritePtr(h Handle, v int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, cl, err := r.resolve(h)
	if err != nil {
		return r.failf("writeptr", err, targetStr(h), v)
	}
	c.dropCellPointer(cellKey{h.sp.frame, h.sp.slot})
	*cl = cell{kind: cellInt, ival: v}
	r.okf("writeptr", v, targetStr(h), c.id)
	return nil
}

// PublishInt snapshots a plain slot into the stack-external global table.
func (r *Runtime) PublishInt(name string, coID int64, frame int64, slot int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, err := r.lookup(coID)
	if err != nil {
		return r.failf("publish-int", err, name, coID, frame, slot)
	}
	cl, err := r.cellAt(c, frame, slot)
	if err != nil {
		return r.failf("publish-int", err, name, coID, frame, slot)
	}
	if cl.kind == cellPtr && cl.pval != nil && cl.pval.dead {
		return r.failf("publish-int", &Error{Kind: ErrDangling, Msg: "slot holds a dangling pointer"}, name)
	}
	if cl.kind == cellPtr {
		return r.failf("publish-int", &Error{Kind: ErrEscape, Msg: "pointer values are never published"}, name)
	}
	r.globals[name] = cl.value()
	r.okf("publish-int", cl.ival, name, coID, frame, slot)
	return nil
}

// PublishPtr always rejects: an intra-stack pointer must never escape to a
// stack-external table. Dangling is reported first per the fixed ordering.
func (r *Runtime) PublishPtr(name string, h Handle) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h.sp == nil {
		return r.failf("publish-ptr", &Error{Kind: ErrArgument, Msg: "nil handle"}, name)
	}
	if h.sp.dead {
		return r.failf("publish-ptr", &Error{Kind: ErrDangling, Msg: "pointer is dangling"}, name, targetStr(h))
	}
	return r.failf("publish-ptr", &Error{Kind: ErrEscape, Msg: "intra-stack pointer must not escape the stack"}, name, targetStr(h))
}

// Globals returns a copy of the externally published plain values.
func (r *Runtime) Globals() map[string]Value {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]Value, len(r.globals))
	for k, v := range r.globals {
		out[k] = v
	}
	return out
}
