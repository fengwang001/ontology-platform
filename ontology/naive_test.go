package ontology

// naiveTable 是按题目规则逐字写成的朴素参考实现，
// 刻意不与正式实现共享任何代码，用于随机对拍。
type naiveTable struct {
	deferrable bool
	deferred   bool

	committed map[string]naiveKey
	txn       *naiveTxn
}

type naiveKey struct {
	null bool
	val  string
}

type naiveTxn struct {
	rows    map[string]naiveKey
	touched map[string]bool
	mode    Mode
}

func newNaive(deferrable, initiallyDeferred bool) *naiveTable {
	return &naiveTable{
		deferrable: deferrable,
		deferred:   initiallyDeferred,
		committed:  map[string]naiveKey{},
	}
}

func (n *naiveTable) begin() Reason {
	if n.txn != nil {
		return ReasonTransactionActive
	}
	rows := map[string]naiveKey{}
	for r, k := range n.committed {
		rows[r] = k
	}
	mode := IMMEDIATE
	if n.deferrable && n.deferred {
		mode = DEFERRED
	}
	n.txn = &naiveTxn{rows: rows, touched: map[string]bool{}, mode: mode}
	return ReasonOK
}

func (n *naiveTable) apply(ops []Op) (Reason, int, string) {
	if n.txn == nil {
		return ReasonNoTransaction, -1, ""
	}

	rows := map[string]naiveKey{}
	for r, k := range n.txn.rows {
		rows[r] = k
	}
	stmtTouched := map[string]bool{}

	for idx, op := range ops {
		switch o := op.(type) {
		case InsertOp:
			if o.Row == "" {
				return ReasonEmptyRow, idx, ""
			}
			if _, exists := rows[o.Row]; exists {
				return ReasonRowExists, idx, ""
			}
			rows[o.Row] = toNaiveKey(o.Key)
			if !n.deferrable {
				if dup, v := naiveDup(rows, o.Row); dup {
					return ReasonUniqueViolation, idx, v
				}
			}
			stmtTouched[o.Row] = true
		case UpdateOp:
			if o.Row == "" {
				return ReasonEmptyRow, idx, ""
			}
			if _, exists := rows[o.Row]; !exists {
				return ReasonRowNotFound, idx, ""
			}
			rows[o.Row] = toNaiveKey(o.Key)
			if !n.deferrable {
				if dup, v := naiveDup(rows, o.Row); dup {
					return ReasonUniqueViolation, idx, v
				}
			}
			stmtTouched[o.Row] = true
		case DeleteOp:
			if o.Row == "" {
				return ReasonEmptyRow, idx, ""
			}
			if _, exists := rows[o.Row]; !exists {
				return ReasonRowNotFound, idx, ""
			}
			delete(rows, o.Row)
		}
	}

	if n.deferrable && n.txn.mode == IMMEDIATE {
		if v, ok := naiveMinViolation(rows, stmtTouched); ok {
			return ReasonUniqueViolation, -1, v
		}
	}

	// 成功：整体生效
	n.txn.rows = rows
	for r := range stmtTouched {
		n.txn.touched[r] = true
	}
	return ReasonOK, -1, ""
}

func (n *naiveTable) setMode(mode Mode) (Reason, string) {
	if n.txn == nil {
		return ReasonNoTransaction, ""
	}
	if mode != IMMEDIATE && mode != DEFERRED {
		return ReasonInvalidMode, ""
	}
	if !n.deferrable {
		return ReasonNotDeferrable, ""
	}
	if mode == IMMEDIATE && n.txn.mode == DEFERRED {
		if v, ok := naiveMinViolation(n.txn.rows, n.txn.touched); ok {
			return ReasonUniqueViolation, v
		}
	}
	n.txn.mode = mode
	return ReasonOK, ""
}

func (n *naiveTable) commit() (Reason, string) {
	if n.txn == nil {
		return ReasonNoTransaction, ""
	}
	if n.deferrable && n.txn.mode == DEFERRED {
		if v, ok := naiveMinViolation(n.txn.rows, n.txn.touched); ok {
			n.txn = nil
			return ReasonUniqueViolation, v
		}
	}
	n.committed = n.txn.rows
	n.txn = nil
	return ReasonOK, ""
}

func (n *naiveTable) rollback() Reason {
	if n.txn == nil {
		return ReasonNoTransaction
	}
	n.txn = nil
	return ReasonOK
}

func (n *naiveTable) keys() []RowKey {
	rows := n.committed
	if n.txn != nil {
		rows = nil
		rows = map[string]naiveKey{}
		for r, k := range n.txn.rows {
			rows[r] = k
		}
	}
	out := []RowKey{}
	for r, k := range rows {
		out = append(out, RowKey{Row: r, Key: fromNaiveKey(k)})
	}
	sortRowKeys(out)
	return out
}

func toNaiveKey(k Key) naiveKey {
	return naiveKey{null: k.IsNull, val: k.Value}
}

func fromNaiveKey(k naiveKey) Key {
	return Key{IsNull: k.null, Value: k.val}
}

func naiveDup(rows map[string]naiveKey, row string) (bool, string) {
	k := rows[row]
	if k.null {
		return false, ""
	}
	for other, otherKey := range rows {
		if other == row || otherKey.null {
			continue
		}
		if otherKey.val == k.val {
			return true, k.val
		}
	}
	return false, ""
}

func naiveMinViolation(rows map[string]naiveKey, checked map[string]bool) (string, bool) {
	counts := map[string]int{}
	for _, k := range rows {
		if !k.null {
			counts[k.val]++
		}
	}
	bad := map[string]bool{}
	for row := range checked {
		k, ok := rows[row]
		if !ok || k.null {
			continue
		}
		if counts[k.val] > 1 {
			bad[k.val] = true
		}
	}
	if len(bad) == 0 {
		return "", false
	}
	var keys []string
	for k := range bad {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys[0], true
}
