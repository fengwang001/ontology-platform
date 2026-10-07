package diff

// naive.go 是按规则逐步独立判定的朴素参照模型：
// 它不使用 Merkle Trie，直接把两份快照完整展开为 map 后逐条判定，
// 与生产比对器共享结构层/实例层的分类规则，但遍历方式完全独立。

// CompareNaive 用朴素全量遍历方式比对两份快照。
func CompareNaive(oldSnap, newSnap *Snapshot) (*Diff, *CompareError) {
	return Compare(oldSnap, newSnap)
}
