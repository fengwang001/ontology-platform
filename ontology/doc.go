// Package ontology implements polymorphic action dispatch for the ontology
// platform: generic action declarations, per-type implementation registration,
// inheritance-based resolution, runtime replacement with in-flight safety,
// relaxation declaration and post-hoc audit, and concurrent revocation
// handling.
//
// Typical flow:
//
//	reg := ontology.NewRegistry()
//	reg.DeclareAction(&ontology.Action{ID: "publish", ...})
//	d := ontology.NewDispatcher(reg)
//
//	// types register their own logic
//	reg.Register("publish", "article", impl, nil)
//	reg.Waive("publish", "draft")                 // explicit defer to parent
//	reg.Replace("publish", "article", impl2, relax, probes)
//
//	out, trace, err := d.Invoke(ctx, instance, "publish", input)
//
// Failure classes are distinguished via errors (ErrNoImplementation,
// ErrExplicitlyWaived, ErrObjectRevoked, ErrPrecondition, ErrPostcondition)
// and via trace.ErrorClass. Every invocation is retrievable from
// Dispatcher.Traces(); registry changes from Registry.MutationLog();
// undeclared behavior differences from Registry.Audit.
//
// See docs/design.md for the design rationale, rejected alternatives and
// verification method.
package ontology
