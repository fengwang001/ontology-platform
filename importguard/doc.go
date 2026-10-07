// Package importguard is an attribute-level permission gatekeeper for batch
// imports on an ontology platform.
//
// A batch declares one Mode (atomic or lenient) and carries many Entry
// records, each targeting an object with create/update semantics and a set of
// property fields. The gatekeeper:
//
//   - rejects the whole batch, before touching any record, when the mode is
//     missing/illegal or the initiating subject is absent at batch start;
//   - judges each record independently with priority: object-type existence >
//     create/update semantic match > field write permissions > post-skip
//     required-property constraints;
//   - in atomic mode fails a whole record (zero writes) if any field is
//     unwritable; in lenient mode skips unwritable fields, writes the rest and
//     re-checks required properties (reusing an existing value only under
//     update semantics), reporting partial success with the skipped fields;
//   - answers all permission questions from an immutable snapshot with a
//     folded latest-revision-wins permission index, so a single field decision
//     is one map lookup independent of batch size or permission history size;
//   - serializes each whole batch under one critical section while judging
//     records concurrently against immutable inputs and committing them in
//     index order, so all executions are equivalent to some global serial
//     order;
//   - logs every decision's inputs, output and basis via DecisionLogger.
//
// See DESIGN.md for the full rationale and the abandoned alternatives.
package importguard
