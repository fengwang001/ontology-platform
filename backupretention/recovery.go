package backupretention

// computeRecoverable 在不重复遍历任何一条祖先链的前提下，标记每个备份是否可恢复。
//
// 可恢复 当且仅当 自身及全部祖先均未损坏。直接“每个备份沿父链走到底”最坏是
// O(n^2)（一条长链）。这里用“每条父边只消费一次”的摊还算法：
//   - 损坏备份立即定案为不可恢复；
//   - 其余备份先沿父指针向上，将经过路径记录在 path 中；
//   - 遇到已定案祖先时，其结论决定整条 path 的结论；
//   - 遇到本次正在处理的祖先（理论上登记保证无环，不会发生）时安全兜底。
//
// 每条父边至多被加入某条 path 一次，因此总时间 O(n + 链总长)，与 n 和深度之和线性。
func computeRecoverable(reg *registry) map[*Backup]bool {
	ok := make(map[*Backup]bool, reg.len())
	for _, b := range reg.all() {
		if _, done := ok[b]; done {
			continue
		}
		if b.Corrupt {
			ok[b] = false
			continue
		}
		var path []*Backup
		cur := b
		for {
			if val, done := ok[cur]; done {
				fillPath(path, ok, val)
				break
			}
			if cur.Corrupt {
				ok[cur] = false
				fillPath(path, ok, false)
				break
			}
			if cur.ParentID == "" {
				ok[cur] = true
				fillPath(path, ok, true)
				break
			}
			parent := reg.byID[cur.ParentID] // 不变量：父必存在
			path = append(path, cur)
			cur = parent
		}
	}
	return ok
}

// fillPath 将一条祖先路径按“从最靠近已定案祖先的节点到最远端”顺序定案。
// 结论向后代传播：最近祖先可恢复，节点本身又未损坏（进入 path 的前提）才可恢复。
func fillPath(path []*Backup, ok map[*Backup]bool, ancestorOK bool) {
	for i := len(path) - 1; i >= 0; i-- {
		node := path[i]
		ancestorOK = ancestorOK && !node.Corrupt
		ok[node] = ancestorOK
	}
}
