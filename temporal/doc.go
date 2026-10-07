// Package temporal is the time-travel graph traversal sub-system of the
// ontology platform.
//
// # Overview
//
// Data lives in an append-only Store. Each Tx.Commit is serialized and
// receives one monotonically increasing Instant. Every object, object type,
// link type and link instance keeps a version timeline; every object's
// outgoing/incoming adjacency is a sequence of persistent (immutable) treap
// roots. A Snapshot pins one Instant and resolves every question against the
// versions that exactly cover that Instant, so a Traverse that starts at a
// historical instant is unaffected by later writes, property-definition
// migrations or cardinality adjustments.
//
// Typical use
//
//	s := temporal.NewStore()
//	tx := s.Begin()
//	tx.CreateObjectType("Person", []temporal.Property{{Name: "name", Type: "string"}})
//	tx.CreateLinkType("knows", temporal.Cardinality{MaxOut: -1})
//	at, _ := tx.Commit()
//
//	tx = s.Begin()
//	tx.CreateObject("a", "Person", temporal.PropertyValues{"name": "Alice"})
//	at, _ = tx.Commit()
//
//	res, err := s.Traverse(temporal.TraversalConfig{
//	    Start:  "a",
//	    At:     at,
//	    Limits: temporal.TraversalLimits{MaxDepth: -1, MaxVisited: -1},
//	}, auditSink)
//
// # Error handling
//
// Traverse returns *TraversalError with one of ErrBeforeHorizon,
// ErrStartNotFound, ErrLimitExceeded and ErrMissingHistory. When several
// conditions coexist the deterministic priority is
// BeforeHorizon > StartNotFound > LimitExceeded > MissingHistory. A failed
// traversal returns no partial result and changes no history.
//
// # Independent verification
//
// NaiveModel is a log-scanning reference implementation sharing no index
// code with Store; differential tests compare both under random histories.
// Snapshot.Stats exposes low-level probe counters to demonstrate that one
// link-existence decision costs a constant number of probes independent of
// the link type's cumulative create/revoke history.
package temporal
