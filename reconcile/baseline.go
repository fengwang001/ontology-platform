package reconcile

// resolveBaseline 确定位点基准：所有可参与副本中最早的逻辑位点。
// 没有任何可参与副本时无法确定基准，返回 ErrInsufficientReplicas。
func resolveBaseline(parts []participant) (uint64, error) {
	if len(parts) == 0 {
		return 0, ErrInsufficientReplicas
	}
	baseline := parts[0].snap.Position
	for _, p := range parts[1:] {
		if p.snap.Position < baseline {
			baseline = p.snap.Position
		}
	}
	return baseline, nil
}
