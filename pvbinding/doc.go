// Package pvbinding implements a deterministic persistent volume and claim
// binding controller.
//
// The controller supports immediate and delayed binding, explicit volume
// choices, reservations, node constraints, reclaim policies and online claim
// expansion. All mutating operations are serialized with one mutex, so every
// concurrent execution is equivalent to one of the possible serial orders.
package pvbinding
