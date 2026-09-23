// Package handle defines the lease credential: a slot number plus a
// generation. A handle is invalidated as soon as it is released.
package handle

// Handle identifies one lease of the resource occupying a slot.
// The generation changes every time the slot is lent out again,
// so a stale handle can never affect a new holder of the same slot.
type Handle struct {
	Slot int
	Gen  uint64
}
