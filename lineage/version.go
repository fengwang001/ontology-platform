package lineage

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

const versionLen = 16 // 取 SHA-256 前 16 个十六进制字符作为版本号

func refKey(r Ref) string { return r.ID + "@" + r.Version }

// sourceVersion 以源对象内容指纹作为版本号；内容不变则版本不变。
func sourceVersion(payload string) string {
	sum := sha256.Sum256([]byte("source\x00" + payload))
	return hex.EncodeToString(sum[:])[:versionLen]
}

// derivedVersion 以“操作 + 全部输入版本 + 输出内容”的指纹作为版本号。
// 相同输入与操作以任意顺序、任意并发时序提交，得到完全相同的版本与血缘。
func derivedVersion(op string, inputs []Ref, payload string) string {
	keys := make([]string, len(inputs))
	for i, in := range inputs {
		keys[i] = refKey(in)
	}
	sort.Strings(keys)
	sum := sha256.Sum256([]byte("derive\x00" + op + "\x00" +
		strings.Join(keys, ",") + "\x00" + payload))
	return hex.EncodeToString(sum[:])[:versionLen]
}

// currentVersionLocked 返回某对象的当前版本。
// 源对象：最后一次登记产生的版本；派生对象：内容指纹最大的版本
// （内容指纹与提交时序无关，保证并发调度顺序不改变结果）。
func (t *Tracker) currentVersionLocked(id string) (string, bool) {
	vers, ok := t.versions[id]
	if !ok || len(vers) == 0 {
		return "", false
	}
	if t.kinds[id] == Source {
		return vers[len(vers)-1], true
	}
	best := vers[0]
	for _, v := range vers[1:] {
		if v > best {
			best = v
		}
	}
	return best, true
}

func (t *Tracker) isCurrentLocked(id, version string) bool {
	cur, ok := t.currentVersionLocked(id)
	return ok && cur == version
}

func (t *Tracker) addNodeLocked(n *Node) {
	if t.nodes[n.ID] == nil {
		t.nodes[n.ID] = map[string]*Node{}
	}
	if _, exists := t.nodes[n.ID][n.Version]; !exists {
		t.nodes[n.ID][n.Version] = n
		t.versions[n.ID] = append(t.versions[n.ID], n.Version)
		t.kinds[n.ID] = n.Kind
	}
}

func (t *Tracker) addEdgeLocked(from, to Ref, op string) {
	if t.out[from.ID] == nil {
		t.out[from.ID] = map[string]map[string]bool{}
	}
	if t.out[from.ID][from.Version] == nil {
		t.out[from.ID][from.Version] = map[string]bool{}
	}
	if t.in[to.ID] == nil {
		t.in[to.ID] = map[string]map[string]bool{}
	}
	if t.in[to.ID][to.Version] == nil {
		t.in[to.ID][to.Version] = map[string]bool{}
	}
	t.out[from.ID][from.Version][refKey(to)] = true
	t.in[to.ID][to.Version][refKey(from)] = true
}

// edgeActiveLocked 判断一条边的两个端点是否仍指向当前版本。
func (t *Tracker) edgeActiveLocked(from, to Ref) bool {
	return t.isCurrentLocked(from.ID, from.Version) &&
		t.isCurrentLocked(to.ID, to.Version)
}

func parseRefKey(key string) Ref {
	id, ver, _ := strings.Cut(key, "@")
	return Ref{ID: id, Version: ver}
}

// uniqueRefs 去除完全相同的引用，保持确定性的排序。
func uniqueRefs(refs []Ref) []Ref {
	seen := map[string]bool{}
	out := make([]Ref, 0, len(refs))
	for _, r := range refs {
		k := refKey(r)
		if !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return refKey(out[i]) < refKey(out[j]) })
	return out
}
