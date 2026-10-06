package contract

func isBlank(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}

// validateCreate 做不依赖合同状态的自洽性检查。
func validateCreate(in CreateContractInput) error {
	if isBlank(in.ID) {
		return errf(ErrInvalidParam, "contract id is blank")
	}
	ids := map[string]bool{}
	for i := 0; i < 2; i++ {
		p := in.Parties[i]
		if isBlank(p.ID) {
			return errf(ErrInvalidParam, "party %d id is blank", i)
		}
		if ids[p.ID] {
			return errf(ErrInvalidParam, "duplicate party id %q", p.ID)
		}
		ids[p.ID] = true
		if p.AuthFrom > p.AuthUntil {
			return errf(ErrInvalidParam, "party %q auth window inverted", p.ID)
		}
	}
	if in.Clauses == nil {
		return errf(ErrInvalidParam, "clauses map is nil")
	}
	for cid := range in.LockedClauses {
		if _, ok := in.Clauses[cid]; !ok {
			return errf(ErrInvalidParam, "locked clause %q not present in main contract", cid)
		}
	}
	for _, name := range []string{ClauseAutoRenew, ClauseRenewalTerm, ClauseNoticeDays} {
		if _, ok := in.Clauses[name]; !ok {
			return errf(ErrInvalidParam, "required renewal clause %q missing", name)
		}
	}
	if in.StartDay > in.ExpiryDay {
		return errf(ErrInvalidParam, "start day %d after expiry day %d", in.StartDay, in.ExpiryDay)
	}
	if in.Clauses[ClauseRenewalTerm] <= 0 {
		return errf(ErrInvalidParam, "renewal term must be positive")
	}
	if in.Clauses[ClauseNoticeDays] < 0 {
		return errf(ErrInvalidParam, "notice days must be non-negative")
	}
	if in.Clauses[ClauseAutoRenew] != 0 && in.Clauses[ClauseAutoRenew] != 1 {
		return errf(ErrInvalidParam, "auto renew flag must be 0 or 1")
	}
	return nil
}

// validateAdd 做自洽性检查；依赖合同的检查（编号是否存在等）在调用方按优先级处理。
func validateAdd(in AddAmendmentInput) error {
	if isBlank(in.ContractID) || isBlank(in.AmendmentID) {
		return errf(ErrInvalidParam, "contract/amendment id is blank")
	}
	hasChanges := len(in.Changes) > 0
	hasRevoke := !isBlank(in.Revokes)
	if hasChanges == hasRevoke {
		return errf(ErrInvalidParam, "exactly one of changes or revokes required")
	}
	for cid := range in.Changes {
		if isBlank(cid) {
			return errf(ErrInvalidParam, "blank clause id in changes")
		}
	}
	return nil
}
