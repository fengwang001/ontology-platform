package temporal

// TraversalLimits are the pre-declared bounds checked during a traversal.
type TraversalLimits struct {
	MaxDepth   int // edges from the start object; -1 = unbounded
	MaxVisited int // number of distinct objects; -1 = unbounded
}

// TraversalConfig parameterizes one traversal.
type TraversalConfig struct {
	Start ObjectID
	At    Instant
	// Limits are pre-declared budgets; exceeding either one fails with
	// ErrLimitExceeded and returns no partial result.
	Limits TraversalLimits
	// LinkTypes restricts expansion to the given link types; nil means all.
	LinkTypes []LinkTypeID
}

// VisitedObject is one object reached by the traversal with its state exactly
// as interpreted at the fixed instant (under the object-type version that was
// in force then).
type VisitedObject struct {
	State ObjectState
	Depth int
}

// TraversalResult is the complete, self-consistent snapshot-scoped output.
type TraversalResult struct {
	At      Instant
	Objects []VisitedObject
	Links   []Link
}

// AuditSink receives one structured record per low-level decision, so a
// traversal can be reconstructed after the fact.
type AuditSink interface {
	Record(rec AuditRecord)
}

// AuditRecord captures the inputs, the fixed baseline, the consulted history
// version and the conclusion of one decision. Every object lookup, every
// candidate-link existence decision and every schema/cardinality version
// consult emits one record.
type AuditRecord struct {
	TraversalID uint64
	At          Instant
	Step        string
	ObjectID    ObjectID
	LinkType    LinkTypeID
	Src, Dst    ObjectID
	Decision    string
	Detail      string
}

// MemoryAudit is a simple in-memory AuditSink used in tests and demos.
type MemoryAudit struct {
	Records []AuditRecord
}

// Record appends one audit record.
func (m *MemoryAudit) Record(rec AuditRecord) { m.Records = append(m.Records, rec) }

// Traverse pins a snapshot at cfg.At, expands the graph from cfg.Start and
// returns one complete result or one classified TraversalError.
//
// Guarantees:
//   - The whole traversal reasons from one immutable snapshot: once pinned,
//     concurrent commits (objects, links, property migrations, cardinality
//     adjustments) cannot change any decision.
//   - On failure it returns no partial result and mutates no history.
//   - When several error conditions coexist it reports exactly one class,
//     in this deterministic order: horizon, then start-not-found, then
//     missing history encountered while answering existence questions, then
//     budget exceeded (budgets are checked against the fully determined
//     expansion, so the answer cannot depend on traversal timing).
func (s *Store) Traverse(cfg TraversalConfig, audit AuditSink) (*TraversalResult, error) {
	id := s.nextTraversalID.next()
	rec := func(step string, fn func(*AuditRecord)) {
		if audit == nil {
			return
		}
		r := AuditRecord{TraversalID: id, At: cfg.At, Step: step}
		fn(&r)
		audit.Record(r)
	}

	// 1) Pin the snapshot first: horizon (and future-instant) errors win.
	sn, err := s.Snapshot(cfg.At)
	if err != nil {
		rec("pin_snapshot", func(r *AuditRecord) {
			r.Decision = "error"
			r.Detail = err.Error()
		})
		return nil, err
	}

	// 2) Start object existence at the fixed instant.
	start, err := sn.Object(cfg.Start)
	rec("lookup_start", func(r *AuditRecord) {
		r.ObjectID = cfg.Start
		if err != nil {
			r.Decision = "error"
			r.Detail = err.Error()
			return
		}
		if start.Exists {
			r.Decision = "exists"
		} else {
			r.Decision = "absent"
		}
	})
	if err != nil {
		return nil, err
	}
	if !start.Exists {
		return nil, &TraversalError{
			Code:   ErrStartNotFound,
			At:     cfg.At,
			Detail: "start object " + string(cfg.Start) + " did not exist",
		}
	}

	// 3) Anchor the property interpretation of the start's type at At.
	if _, _, err := sn.ObjectTypeProps(start.Type); err != nil {
		return nil, err
	}

	// 4) Deterministic breadth-first expansion entirely against the pinned
	// snapshot. Every object reached has its type definition anchored at At;
	// every link expanded is a link that existed at At regardless of the
	// cardinality rule currently in force.
	allow := map[LinkTypeID]bool{}
	for _, lt := range cfg.LinkTypes {
		allow[lt] = true
	}

	type queueItem struct {
		id    ObjectID
		depth int
	}
	depthOf := map[ObjectID]int{cfg.Start: 0}
	order := []ObjectID{cfg.Start}
	var links []Link

	queue := []queueItem{{cfg.Start, 0}}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		if cfg.Limits.MaxDepth >= 0 && item.depth >= cfg.Limits.MaxDepth {
			if cfg.Limits.MaxDepth == 0 {
				return nil, &TraversalError{
					Code:   ErrLimitExceeded,
					At:     cfg.At,
					Detail: "start node cannot be expanded with MaxDepth=0",
				}
			}
			rec("depth_boundary", func(r *AuditRecord) {
				r.ObjectID = item.id
				r.Decision = "do_not_expand"
				r.Detail = "node already at MaxDepth"
			})
			continue
		}

		var iterErr error
		enumErr := sn.LinksFrom(item.id, "", func(l Link) bool {
			if len(allow) > 0 && !allow[l.Type] {
				rec("filter_link_type", func(r *AuditRecord) {
					r.LinkType, r.Src, r.Dst = l.Type, l.Src, l.Dst
					r.Decision = "filtered_out"
				})
				return true
			}
			rec("link_exists", func(r *AuditRecord) {
				r.LinkType, r.Src, r.Dst = l.Type, l.Src, l.Dst
				r.Decision = "exists_at_baseline"
			})
			links = append(links, l)
			if _, seen := depthOf[l.Dst]; !seen {
				childDepth := item.depth + 1
				if cfg.Limits.MaxDepth >= 0 && childDepth > cfg.Limits.MaxDepth {
					iterErr = &TraversalError{
						Code:   ErrLimitExceeded,
						At:     cfg.At,
						Detail: "next hop to " + string(l.Dst) + " exceeds MaxDepth",
					}
					return false
				}
				if cfg.Limits.MaxVisited >= 0 && len(order)+1 > cfg.Limits.MaxVisited {
					iterErr = &TraversalError{
						Code:   ErrLimitExceeded,
						At:     cfg.At,
						Detail: "visiting next object exceeds MaxVisited",
					}
					return false
				}
				depthOf[l.Dst] = childDepth
				order = append(order, l.Dst)
				queue = append(queue, queueItem{l.Dst, childDepth})
			}
			return true
		})
		if iterErr != nil {
			// The callback already produced a classified error (budget);
			// the sentinel from LinksFrom must not replace it.
			return nil, iterErr
		}
		iterErr = enumErr
		if iterErr != nil {
			if _, stopped := iterErr.(callbackStopped); stopped {
				continue
			}
			if te, ok := iterErr.(*TraversalError); ok && te.Code == ErrLimitExceeded {
				rec("limit_check", func(r *AuditRecord) {
					r.Decision = "limit_exceeded"
					r.Detail = te.Detail
				})
				return nil, iterErr
			}
			rec("enumerate_links", func(r *AuditRecord) {
				r.ObjectID = item.id
				r.Decision = "error"
				r.Detail = iterErr.Error()
			})
			return nil, iterErr
		}
	}

	// 5) Resolve every reached object and anchor its type definition at At.
	// Doing this after the shape is fixed means schema data needed for the
	// result is fetched once and a missing-history gap is reported as the
	// MissingHistory class without leaking a half-built result.
	if cfg.Limits.MaxVisited >= 0 && len(order) > cfg.Limits.MaxVisited {
		// Unreachable through normal BFS (the check is enforced while
		// discovering children), but kept to also cover the degenerate
		// MaxVisited=0 request with an existing start object.
		return nil, &TraversalError{Code: ErrLimitExceeded, At: cfg.At, Detail: "result exceeds MaxVisited"}
	}
	objects := make([]VisitedObject, 0, len(order))
	for _, id := range order {
		st, err := sn.Object(id)
		if err != nil {
			return nil, err
		}
		props, _, err := sn.ObjectTypeProps(st.Type)
		if err != nil {
			return nil, err
		}
		rec("anchor_type_version", func(r *AuditRecord) {
			r.ObjectID = id
			r.LinkType = LinkTypeID(st.Type)
			r.Decision = "property_definition_anchored_at_baseline"
			r.Detail = propertyNames(props)
		})
		objects = append(objects, VisitedObject{State: st, Depth: depthOf[id]})
	}

	return &TraversalResult{At: cfg.At, Objects: objects, Links: links}, nil
}

func propertyNames(props []Property) string {
	out := ""
	for i, p := range props {
		if i > 0 {
			out += ","
		}
		out += p.Name
	}
	return out
}
