package ontology

import "fmt"

// OpKind identifies the kind of a batch operation.
type OpKind int

const (
	OpCreateLink OpKind = iota
	OpDeleteLink
	OpDeleteObject
)

func (k OpKind) String() string {
	switch k {
	case OpCreateLink:
		return "CreateLink"
	case OpDeleteLink:
		return "DeleteLink"
	case OpDeleteObject:
		return "DeleteObject"
	}
	return "Unknown"
}

// Op is one batch operation. Object is used by OpDeleteObject; the link
// fields by the other kinds.
type Op struct {
	Kind     OpKind
	LinkType string
	Source   string
	Target   string
	Object   string
}

func (o Op) String() string {
	if o.Kind == OpDeleteObject {
		return fmt.Sprintf("%s(%s)", o.Kind, o.Object)
	}
	return fmt.Sprintf("%s(%s %s -> %s)", o.Kind, o.LinkType, o.Source, o.Target)
}

// Batch collects operations applied atomically by Store.ApplyBatch.
//
// Semantics inside a batch (deterministic, applied in order):
//   - CreateLink of an already-existing link (pre-existing or created
//     earlier in the same batch) is a no-op, not a duplicate error.
//   - DeleteLink of a missing link is a no-op; create-then-delete of the
//     same link leaves it absent.
//   - DeleteObject cascades over the state as it evolves, so a link created
//     earlier in the batch is still subject to a later cascade delete.
//   - Cardinality and endpoint violations abort the whole batch: nothing is
//     applied and the failing op is reported via *BatchError.
type Batch struct {
	ops []Op
}

// CreateLink appends a create-link operation.
func (b *Batch) CreateLink(linkType, source, target string) *Batch {
	b.ops = append(b.ops, Op{Kind: OpCreateLink, LinkType: linkType, Source: source, Target: target})
	return b
}

// DeleteLink appends a delete-link operation.
func (b *Batch) DeleteLink(linkType, source, target string) *Batch {
	b.ops = append(b.ops, Op{Kind: OpDeleteLink, LinkType: linkType, Source: source, Target: target})
	return b
}

// DeleteObject appends a delete-object operation (with cascade settlement).
func (b *Batch) DeleteObject(id string) *Batch {
	b.ops = append(b.ops, Op{Kind: OpDeleteObject, Object: id})
	return b
}

// Len returns the number of queued operations.
func (b *Batch) Len() int { return len(b.ops) }

// ApplyBatch applies every operation against a private copy of the state and
// swaps it in only if all succeed. The live state is never partially
// modified, and concurrent readers keep seeing a consistent snapshot.
func (s *Store) ApplyBatch(b *Batch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	work := s.st.clone()
	for i, op := range b.ops {
		if err := applyOp(work, op); err != nil {
			return &BatchError{Index: i, Op: op, Err: err}
		}
	}
	s.st = work
	return nil
}

func applyOp(st *state, op Op) error {
	switch op.Kind {
	case OpCreateLink:
		return createLink(st, op.LinkType, op.Source, op.Target, true)
	case OpDeleteLink:
		if _, ok := st.linkTypes[op.LinkType]; !ok {
			return fmt.Errorf("unknown link type %q", op.LinkType)
		}
		st.removeLinkRaw(op.LinkType, op.Source, op.Target)
		return nil
	case OpDeleteObject:
		plan, err := planDelete(st, op.Object)
		if err != nil {
			return err
		}
		applyDelete(st, plan)
		return nil
	}
	return fmt.Errorf("unknown op kind %d", op.Kind)
}
