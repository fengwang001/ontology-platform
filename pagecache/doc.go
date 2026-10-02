// Package pagecache implements a shared page cache whose physical page
// capacity is charged to exactly one owning cgroup per page, even when several
// cgroups reference the same page.
//
// # References, holders and the owner
//
// A mapping is a named, non-empty sequence of pages; the same page ID may
// occur multiple times in one mapping, and every occurrence is one reference.
// ref(c, id) is the total number of occurrences of page id across all of
// cgroup c's current mappings. A cgroup with ref > 0 is a holder of the page.
// since(c, id) is the clock value of the most recent operation that took ref
// from 0 to a positive number; it does not change while ref stays positive and
// is re-stamped whenever ref later becomes positive again after reaching 0.
//
// Among the holders of a page, the owner is the holder ordered first by
// (since ascending, cgroup name byte order ascending). A page with no holders
// is reclaimed: the cache forgets its size, so the same ID can later be stored
// again with a different size. Each live page's size is charged once, to its
// owner only. used(c) sums the sizes of pages owned by c, Logical(c) sums the
// sizes of distinct pages held by c, and Total sums all live page sizes;
// therefore the sum of all used values always equals Total.
//
// Map admission: delta, limit and capacity
//
// For each distinct page referenced by Map, the owner is computed as it will
// be after the operation. A page contributes its size to delta exactly when
// the post-operation owner is the mapping's cgroup while the pre-operation
// owner was not; this covers brand-new pages and takeovers that happen
// because two holders share the same since and the new cgroup has a smaller
// name. fresh sums sizes of distinct pages that do not currently exist in the
// cache. Admission checks, in order, are:
//
//	Total + fresh > capacity  -> ErrCapacity (equality passes)
//	delta > 0 && used+delta > limit -> ErrLimit (equality passes)
//
// A Map with delta == 0 (purely referencing existing pages without taking
// ownership) is always admitted even if the cgroup is already over its limit;
// in that case fresh is necessarily 0 as well. fresh counts each distinct new
// page once regardless of how many times it occurs in the mapping.
//
// # Unmap and ownership migration
//
// Unmap is never rejected for limit or capacity reasons. References of the
// removed mapping are deducted per page; a cgroup whose ref reaches 0 stops
// being a holder. If that cgroup owned the page and other holders remain,
// ownership migrates to the remaining holder with the smallest
// (since, name), and the page size moves from the old owner's used to the new
// owner's used (the receiver may become over limit). If no holders remain,
// the page is reclaimed and Total drops by its size.
//
// All operations are serialized by a single mutex, so concurrent callers
// observe an atomic, serializable history; Used, Logical, Over and Total read
// incrementally maintained counters in O(1) without rescanning mappings.
package pagecache
