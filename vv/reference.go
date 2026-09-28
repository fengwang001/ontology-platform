package vv

// NaiveDiff 是朴素参照：发送来源全部日志（不做版本向量裁剪）。
func NaiveDiff(source *Replica) []Change {
	return nil
}

// Converged 判定两个副本的向量、日志与视图是否完全一致。
func Converged(a, b *Replica) bool {
	return false
}
