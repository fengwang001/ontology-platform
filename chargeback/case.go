package chargeback

const noDay = -1

// Case 是拒付案件的持久记录。只记录被接受操作产生的事件，
// 状态永远由事件与查询时刻 now 推导，不存储会随时间漂移的状态字段。
type Case struct {
	ID           string
	TxnID        string
	MerchantID   string // 冗余自交易，便于资金事件直接入账
	Reason       Reason
	Amount       int64
	OpenDay      int
	RespondDay   int     // 未应诉为 noDay
	PreArbDay    int     // 未发起预仲裁为 noDay
	Accepted     bool    // 发卡行已显式接受应诉（立即商户胜）
	Ruling       Outcome // 未裁决为 OutcomeNone
	MoneySettled bool    // 扣回款是否已终局归属（防止计划事件重复结算）
	BasisTxnID   string  // 重复扣款的依据交易，其他原因为空
}

// State 推导案件在 now 时刻的状态。纯函数，不依赖任何物化缓存。
func (c *Case) State(now int, cfg Config) State {
	if c.Ruling != OutcomeNone {
		return closedState(c.Ruling)
	}
	if c.Accepted {
		return StateClosedMerchantWin
	}
	if c.PreArbDay != noDay {
		return StatePreArbitration
	}
	if c.RespondDay != noDay {
		if now > c.RespondDay+cfg.ReviewWindowDays {
			// 审阅期逾期：视为发卡行接受应诉，商户胜。
			return StateClosedMerchantWin
		}
		return StateAwaitingReview
	}
	if now > c.OpenDay+cfg.ResponseWindowDays {
		// 应诉期逾期：商户放弃，拒付成立，发卡行胜。
		return StateClosedIssuerWin
	}
	return StateOpened
}

func closedState(o Outcome) State {
	if o == OutcomeMerchantWin {
		return StateClosedMerchantWin
	}
	return StateClosedIssuerWin
}

// countsAgainstDisputable 报告案件在 now 时刻是否占用交易的可拒付余额：
// 进行中或以发卡行胜告终的案件占用；商户胜告终的释放。
func (c *Case) countsAgainstDisputable(now int, cfg Config) bool {
	return c.State(now, cfg) != StateClosedMerchantWin
}
