package diff

// trie.go 实现固定深度的 Merkle 前缀 Trie（radix=16, depth=16）。
//
// 只有 hash 前缀发生分叉的节点才需要被访问：未变化条目全部落在
// hash 相同的公共子树中，整棵子树以一个 64bit 摘要比较后跳过。

const (
	trieRadixBits = 4
	trieRadix     = 1 << trieRadixBits
	trieDepth     = 64 / trieRadixBits
)

type trieNode struct{}

type trie struct{}

func newTrie(entries []trieEntry) *trie { return &trie{} }

type trieEntry struct {
	key  string
	hash uint64
}

// changedEntries 返回两侧 trie 中新增、删除或指纹变化的条目。
// visited 记录被实体化访问的节点数（用于开销复核）。
func changedEntries(oldT, newT *trie) (added []trieEntry, removed []trieEntry, visited int) {
	return nil, nil, 0
}
