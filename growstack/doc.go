// Package growstack implements a growable, shrinkable, relocating per-
// coroutine stack with tracked intra-stack pointers.
//
// The package is split into cooperating modules:
//
//   - config.go / errors.go: configuration validation (anti-thrash and
//     reachability checks) and the fixed error taxonomy and precedence.
//   - runtime.go: ownership, global mutex, quota accounting, allocator
//     abstraction and coherent statistics snapshots.
//   - registry.go: coroutine/frame registry and every public operation
//     (push/pop, slot read/write, pointer mint/copy/deref, escape publish).
//   - pointer.go: per-coroutine live-pointer set and per-target inbound
//     index, giving O(1) non-relocating operations and O(pointers) fixup.
//   - grow.go: minimal-multiple growth, threshold-driven shrink and the
//     relocation with pointer fixup.
//
// Intra-stack pointers carry a logical target (coroutine id, monotonic
// frame id, slot). Handles returned to callers are opaque and never resolve
// to a raw machine address, so a moving stack cannot invalidate them; only
// popping the target frame makes a pointer dangling.
package growstack
