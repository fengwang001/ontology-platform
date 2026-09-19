package ontology

import "fmt"

// undoStep is one reversible mutation; desc identifies it in inconsistency
// reports when compensation itself fails.
type undoStep struct {
	desc string
	undo func() error
}

// Tx is the transaction context of one top-level action execution. Nested
// actions share the same Tx and are isolated by savepoints.
type Tx struct {
	engine      *Engine
	store       *Store
	undos       []undoStep
	chain       []string
	affectedObj map[string]bool
	affectedRel map[string]bool
}

// Chain returns the current action call chain, outermost first.
func (tx *Tx) Chain() []string {
	return append([]string(nil), tx.chain...)
}

// CreateObject adds a new object; it fails if the id is taken.
func (tx *Tx) CreateObject(id, typ string, props map[string]any) error {
	if _, ok := tx.store.getObject(id); ok {
		return fmt.Errorf("object %q already exists", id)
	}
	cp := copyObject(&Object{ID: id, Type: typ, Props: props})
	tx.store.addObject(&cp)
	tx.undos = append(tx.undos, undoStep{
		desc: "create object " + id,
		undo: func() error {
			if err := tx.store.undoFault(id); err != nil {
				return err
			}
			if _, ok := tx.store.getObject(id); !ok {
				return fmt.Errorf("object %q missing during undo", id)
			}
			tx.store.removeObject(id)
			return nil
		},
	})
	tx.affectedObj[id] = true
	return nil
}

// SetProperty sets one property on an existing object.
func (tx *Tx) SetProperty(id, key string, val any) error {
	o, ok := tx.store.getObject(id)
	if !ok {
		return fmt.Errorf("object %q not found", id)
	}
	old, had := o.Props[key]
	tx.store.setProp(id, key, val)
	tx.undos = append(tx.undos, undoStep{
		desc: fmt.Sprintf("set property %s.%s", id, key),
		undo: func() error {
			if err := tx.store.undoFault(id); err != nil {
				return err
			}
			if _, ok := tx.store.getObject(id); !ok {
				return fmt.Errorf("object %q missing during undo", id)
			}
			if had {
				tx.store.setProp(id, key, old)
			} else {
				tx.store.delProp(id, key)
			}
			return nil
		},
	})
	tx.affectedObj[id] = true
	return nil
}

// DeleteObject removes an object together with every relation touching it.
func (tx *Tx) DeleteObject(id string) error {
	o, ok := tx.store.getObject(id)
	if !ok {
		return fmt.Errorf("object %q not found", id)
	}
	objCopy := copyObject(o)
	rels := tx.store.RelationsOf(id)
	for _, r := range rels {
		tx.store.removeRelation(r.ID)
	}
	tx.store.removeObject(id)
	tx.undos = append(tx.undos, undoStep{
		desc: "delete object " + id,
		undo: func() error {
			if err := tx.store.undoFault(id); err != nil {
				return err
			}
			restore := objCopy
			tx.store.addObject(&restore)
			for _, r := range rels {
				rel := r
				tx.store.addRelation(&rel)
			}
			return nil
		},
	})
	tx.affectedObj[id] = true
	for _, r := range rels {
		tx.affectedRel[r.ID] = true
	}
	return nil
}

// Link creates a typed relation between two existing objects.
func (tx *Tx) Link(id, typ, from, to string) error {
	if _, ok := tx.store.getObject(from); !ok {
		return fmt.Errorf("object %q not found", from)
	}
	if _, ok := tx.store.getObject(to); !ok {
		return fmt.Errorf("object %q not found", to)
	}
	tx.store.mu.RLock()
	_, dup := tx.store.relations[id]
	tx.store.mu.RUnlock()
	if dup {
		return fmt.Errorf("relation %q already exists", id)
	}
	tx.store.addRelation(&Relation{ID: id, Type: typ, From: from, To: to})
	tx.undos = append(tx.undos, undoStep{
		desc: "link " + id,
		undo: func() error {
			if err := tx.store.undoFault(id); err != nil {
				return err
			}
			tx.store.removeRelation(id)
			return nil
		},
	})
	tx.affectedRel[id] = true
	return nil
}

// Unlink removes an existing relation.
func (tx *Tx) Unlink(id string) error {
	tx.store.mu.RLock()
	r, ok := tx.store.relations[id]
	tx.store.mu.RUnlock()
	if !ok {
		return fmt.Errorf("relation %q not found", id)
	}
	rel := *r
	tx.store.removeRelation(id)
	tx.undos = append(tx.undos, undoStep{
		desc: "unlink " + id,
		undo: func() error {
			if err := tx.store.undoFault(id); err != nil {
				return err
			}
			restore := rel
			tx.store.addRelation(&restore)
			return nil
		},
	})
	tx.affectedRel[id] = true
	return nil
}

// ExecuteAction invokes a nested action. On failure only the nested part is
// rolled back to the savepoint; the caller may continue or give up.
func (tx *Tx) ExecuteAction(name string, params map[string]any) error {
	sp := tx.savepoint()
	if _, err := tx.engine.runAction(tx, name, params); err != nil {
		tx.rollbackTo(sp)
		return err
	}
	return nil
}

func (tx *Tx) savepoint() int {
	return len(tx.undos)
}

// rollbackTo undoes steps in strict reverse order down to sp. A failing
// undo step is recorded and marks the store inconsistent; the remaining
// steps are still attempted so the divergence is fully known.
func (tx *Tx) rollbackTo(sp int) {
	for i := len(tx.undos) - 1; i >= sp; i-- {
		if err := tx.undos[i].undo(); err != nil {
			tx.store.markInconsistent(tx.undos[i].desc, err)
		}
	}
	tx.undos = tx.undos[:sp]
}

func (tx *Tx) rollback() {
	tx.rollbackTo(0)
}
