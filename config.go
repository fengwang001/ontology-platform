package ontology

import "fmt"

// Config 描述续签窗口、答复期、终止通知期与涨幅档位。
//   - 要约窗口：距终止日 [A, B] 天，两端取等均在窗口内；
//   - 答复期：发出日起 C 天内（截止日当天仍可答复）；
//   - 终止通知：通知日之后第 D 天生效；
//   - 每满一整年，租金上限累加一档 stepBP（基点，1bp=0.01%）；
//     不足一年为 0 档；累加金额不得超过 capBP 对应的封顶比例。
type Config struct {
	A, B   int
	C      int
	D      int
	StepBP int // 每满一年累加一档（基点）
	CapBP  int // 封顶涨幅（基点）
}

func (c Config) validate() error {
	if c.A < 0 || c.B < c.A || c.C < 0 || c.D < 0 || c.StepBP < 0 || c.CapBP < 0 {
		return fmt.Errorf("invalid config: window=[%d,%d] reply=%d notice=%d step=%d cap=%d",
			c.A, c.B, c.C, c.D, c.StepBP, c.CapBP)
	}
	return nil
}

// rentCap 返回 currentRent 在 issueDay 发出要约时允许的最高租金（整数分）。
// 已满整年数 n 按 365 天/年向下取整；不足一整年时额度为 0。
// 金额计算全程整数：可涨额度 = floor(rent*rate/10000)，不足一分不计。
func rentCap(currentRent, lastAdjustDay, issueDay, stepBP, capBP int) int {
	if currentRent <= 0 || issueDay < lastAdjustDay {
		return currentRent
	}
	years := (issueDay - lastAdjustDay) / 365
	rate := years * stepBP
	if rate > capBP {
		rate = capBP
	}
	allowance := currentRent * rate / 10000
	return currentRent + allowance
}
