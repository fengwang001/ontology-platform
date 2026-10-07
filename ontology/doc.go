// Package ontology implements traversal permission resolution and shortest
// path queries over an ontology link graph.
//
// Traversal permission for a single link is determined by two declarative
// layers:
//
//   - object-type layer: a permission group's default verdict for a whole
//     object type (Allow or Deny);
//   - link-type layer: a permission group's verdict for a concrete link type
//     (Allow, Deny, or Unset).
//
// A subject belongs to a set of permission groups. The group with the highest
// priority number wins; contradictory declarations from groups sharing the
// highest priority are merged with deny-wins. A link-type declaration, when
// present, overrides the object-type default.
package ontology
