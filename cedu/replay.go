package cedu

// replay 在全新服务实例上重放 events 中 at <= asOf 的被接受操作，
// 返回该历史时刻的快照。重放使用与在线路径完全相同的公开方法，
// 因而“当时的实际”与日志事实可逐字节核对。
func replay(cfg Config, events []event, asOf int) (HolderStatus, error) {
	svc, err := NewService(cfg)
	if err != nil {
		return HolderStatus{}, err
	}
	var hid string
	for _, e := range events {
		if e.at > asOf {
			break
		}
		switch e.kind {
		case evRegister:
			hid = ""
			// 重放时注册 ID 固定为占位 ID（快照只依赖周期数据）。
			if err := svc.RegisterHolder(RegisterInput{HolderID: histHolderID, IssueDate: e.issue, Now: e.at}); err != nil {
				return HolderStatus{}, err
			}
			hid = histHolderID
		case evCredit:
			if _, err := svc.RegisterCredit(CreditInput{
				HolderID: hid, Category: e.cat, Credits: e.credits,
				EarnedOn: e.earnedOn, Org: e.org, Now: e.at,
			}); err != nil {
				return HolderStatus{}, err
			}
		case evCorrect:
			if err := svc.CorrectCredit(hid, mapReplayID(e.recordID), e.org, e.credits, e.at); err != nil {
				return HolderStatus{}, err
			}
		case evRevoke:
			if err := svc.RevokeCredit(hid, mapReplayID(e.recordID), e.org, e.at); err != nil {
				return HolderStatus{}, err
			}
		}
	}
	return svc.Status(hid, asOf)
}

const histHolderID = "__history__"

// mapReplayID 把在线记录 ID 的持证人前缀替换为重放占位前缀。
func mapReplayID(id string) string {
	return histHolderID + id[indexByte(id, '-'):]
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return 0
}
