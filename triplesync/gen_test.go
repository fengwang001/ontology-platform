package triplesync

import (
	"fmt"
	"math/rand"
)

type genNode struct {
	parent int64
	name   string
	dir    bool
}

// genWorld builds a random valid base tree, then derives local and remote
// snapshots by independent random edits. Edits include moves that can create
// cycles/dangling parents in merged states; per-side snapshots stay valid.
func genWorld(rng *rand.Rand) (base, local, remote Snapshot) {
	n := 1 + rng.Intn(10)
	dirs := []int64{0}
	nodes := map[int64]genNode{}
	nameSeq := 0
	nextName := func() string {
		nameSeq++
		return fmt.Sprintf("n%d", nameSeq)
	}
	for id := int64(1); id <= int64(n); id++ {
		isDir := rng.Intn(3) == 0 || len(dirs) == 1 && rng.Intn(2) == 0
		parent := dirs[rng.Intn(len(dirs))]
		nodes[id] = genNode{parent: parent, name: nextName(), dir: isDir}
		if isDir {
			dirs = append(dirs, id)
		}
	}
	toSnap := func(m map[int64]genNode, hashes map[int64]string) Snapshot {
		s := Snapshot{}
		for id, nd := range m {
			e := Entry{Parent: nd.parent, Name: nd.name, Dir: nd.dir}
			if !nd.dir {
				e.Hash = hashes[id]
			}
			s[id] = e
		}
		return s
	}
	hashes := map[int64]string{}
	for id, nd := range nodes {
		if !nd.dir {
			hashes[id] = fmt.Sprintf("h%dv0", id)
		}
	}
	base = toSnap(nodes, hashes)

	cloneNodes := func() map[int64]genNode {
		c := make(map[int64]genNode, len(nodes))
		for k, v := range nodes {
			c[k] = v
		}
		return c
	}
	cloneHashes := func() map[int64]string {
		c := make(map[int64]string, len(hashes))
		for k, v := range hashes {
			c[k] = v
		}
		return c
	}

	mutate := func() (map[int64]genNode, map[int64]string) {
		m := cloneNodes()
		h := cloneHashes()
		steps := rng.Intn(6)
		nextID := int64(n + 1)
		allDirs := func() []int64 {
			d := []int64{0}
			for id, nd := range m {
				if nd.dir {
					d = append(d, id)
				}
			}
			return d
		}
		uniqueName := func(parent int64) string {
			nameSeq++
			name := fmt.Sprintf("n%d", nameSeq)
			for {
				coll := false
				for _, nd := range m {
					if nd.parent == parent && nd.name == name {
						coll = true
						break
					}
				}
				if !coll {
					return name
				}
				nameSeq++
				name = fmt.Sprintf("n%d", nameSeq)
			}
		}
		for step := 0; step < steps; step++ {
			ids := make([]int64, 0, len(m))
			for id := range m {
				ids = append(ids, id)
			}
			switch rng.Intn(6) {
			case 0: // delete subtree
				if len(ids) == 0 {
					continue
				}
				victim := ids[rng.Intn(len(ids))]
				// Delete victim and descendants to keep the snapshot valid.
				drop := map[int64]bool{victim: true}
				changed := true
				for changed {
					changed = false
					for id, nd := range m {
						if drop[nd.parent] && !drop[id] {
							drop[id] = true
							changed = true
						}
					}
				}
				for id := range drop {
					delete(m, id)
					delete(h, id)
				}
			case 1: // create
				ds := allDirs()
				parent := ds[rng.Intn(len(ds))]
				isDir := rng.Intn(2) == 0
				id := nextID
				nextID++
				name := uniqueName(parent)
				m[id] = genNode{parent: parent, name: name, dir: isDir}
				if !isDir {
					h[id] = fmt.Sprintf("h%dv%d", id, rng.Intn(3))
				}
			case 2: // rename / move
				if len(ids) == 0 {
					continue
				}
				id := ids[rng.Intn(len(ids))]
				nd := m[id]
				if rng.Intn(2) == 0 {
					nd.name = uniqueName(nd.parent)
				} else {
					ds := allDirs()
					np := ds[rng.Intn(len(ds))]
					// Avoid moving a directory into itself/descendant to keep
					// this side valid.
					if nd.dir {
						bad := map[int64]bool{id: true}
						ch := true
						for ch {
							ch = false
							for oid, ond := range m {
								if bad[ond.parent] && !bad[oid] {
									bad[oid] = true
									ch = true
								}
							}
						}
						if bad[np] {
							continue
						}
					}
					name := uniqueName(np)
					nd.parent = np
					nd.name = name
				}
				m[id] = nd
			case 3: // change file hash
				var files []int64
				for id, nd := range m {
					if !nd.dir {
						files = append(files, id)
					}
				}
				if len(files) == 0 {
					continue
				}
				id := files[rng.Intn(len(files))]
				h[id] = fmt.Sprintf("h%dv%d", id, 1+rng.Intn(4))
			case 4: // create colliding name on purpose
				ds := allDirs()
				parent := ds[rng.Intn(len(ds))]
				var target string
				for _, nd := range m {
					if nd.parent == parent {
						target = nd.name
						break
					}
				}
				if target == "" {
					continue
				}
				id := nextID
				nextID++
				isDir := rng.Intn(2) == 0
				m[id] = genNode{parent: parent, name: target, dir: isDir}
				if !isDir {
					h[id] = fmt.Sprintf("h%dv0", id)
				}
			case 5: // create ".c"-style name to force x-append chains
				ds := allDirs()
				parent := ds[rng.Intn(len(ds))]
				id := nextID
				nextID++
				name := fmt.Sprintf("a.c%d", id)
				// Also create a sibling literally named "a" so the loser
				// candidate .c<id> may already exist.
				m[id] = genNode{parent: parent, name: name, dir: false}
				h[id] = fmt.Sprintf("h%dv0", id)
			}
		}
		return m, h
	}

	ml, hl := mutate()
	mr, hr := mutate()
	local = toSnap(ml, hl)
	remote = toSnap(mr, hr)

	// Occasionally force same-name creates across sides at same parent.
	if rng.Intn(3) == 0 {
		commonDirs := []int64{0}
		for id, e := range local {
			if e.Dir {
				if re, ok := remote[id]; ok && re.Dir {
					commonDirs = append(commonDirs, id)
				}
			}
		}
		idL, idR := int64(1000+rng.Intn(100)), int64(2000+rng.Intn(100))
		parent := commonDirs[rng.Intn(len(commonDirs))]
		local[idL] = Entry{Parent: parent, Name: "shared", Hash: "Lx"}
		remote[idR] = Entry{Parent: parent, Name: "shared", Hash: "Ry"}
	}
	return base, local, remote
}
