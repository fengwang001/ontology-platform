package narledger

// validate 基础参数合法性：非空字符串与时间/数量取值域。
// 各操作的专属取值域（如结清允许 0）在操作处另行判定。

func nonEmpty(op, field, v string) *OpError {
	if v == "" {
		return opError(op, ErrInvalidParam, "参数 %s 为空字符串", field)
	}
	return nil
}

func nonEmptyMany(op string, fields map[string]string) *OpError {
	for name, v := range fields {
		if err := nonEmpty(op, name, v); err != nil {
			return err
		}
	}
	return nil
}

// positiveQty 校验 1..10^6 的支数（入库、领用、销毁）。
func positiveQty(op string, qty int64) *OpError {
	if qty < 1 || qty > 1_000_000 {
		return opError(op, ErrInvalidParam, "数量 %d 不在 [1,10^6]", qty)
	}
	return nil
}

// settleQty 结清三项允许 0..10^6。
func settleQty(op string, used, returned, residue int64) *OpError {
	for name, v := range map[string]int64{"used": used, "returned": returned, "residue": residue} {
		if v < 0 || v > 1_000_000 {
			return opError(op, ErrInvalidParam, "%s=%d 不在 [0,10^6]", name, v)
		}
	}
	return nil
}

func validNow(op string, now int64) *OpError {
	if now < 0 || now > 1_000_000_000 {
		return opError(op, ErrInvalidParam, "now=%d 不在 [0,10^9]", now)
	}
	return nil
}

// checkReviewers 按错误优先级校验两名复核人：
// 人数不足/同一人/为申请人本人 -> 授权无效。
func (l *Ledger) checkReviewers(op string, now int64, applicant string, r1, r2 string) *OpError {
	if r1 == "" || r2 == "" {
		return opError(op, ErrReviewer, "复核人人数不足: %q,%q", r1, r2)
	}
	if r1 == r2 {
		return opError(op, ErrReviewer, "两名复核人为同一人 %s", r1)
	}
	if applicant != "" && (r1 == applicant || r2 == applicant) {
		return opError(op, ErrReviewer, "复核人不得是申请人本人 %s", applicant)
	}
	if !l.auth.validAt(r1, now) {
		return opError(op, ErrUnauthorized, "复核人 %s 在 now=%d 授权无效", r1, now)
	}
	if !l.auth.validAt(r2, now) {
		return opError(op, ErrUnauthorized, "复核人 %s 在 now=%d 授权无效", r2, now)
	}
	return nil
}
