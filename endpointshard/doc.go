// Package endpointshard maintains service endpoints in fixed-capacity
// shards.
//
// A Manager owns named services. Each service places its endpoints in
// numbered shards of at most M endpoints; shard numbers start at 1,
// increase monotonically and are never reused. Sync reconciles the
// shards with a desired endpoint set while keeping surviving endpoints
// in their original shard, placing new endpoints into the fullest
// non-full shard (ties to the lowest number), deleting empty shards
// and merging at most one pair of shards per call. Resize changes M
// atomically. Query serves consumer reads with a ready-first,
// draining-fallback selection and same-region priority.
//
// Every mutation returns a SyncReport describing exactly the shards
// that changed, were created or were deleted, and bumps the
// modification generation of precisely those shards. All operations
// are linearizable; operations on one service never interleave.
//
// See docs/endpointshard-design.md for the design rationale, the cost
// model and the rejected alternatives.
package endpointshard
