package booking

type Voucher struct {
	ID        string
	Owner     string
	Amount    int64
	ExpiresAt int64
	Used      bool
}

func (v Voucher) usable(owner string, now int64) ErrorKind {
	if v.Owner != owner {
		return ErrVoucherOwner
	}
	if now >= v.ExpiresAt {
		return ErrVoucherExpired
	}
	if v.Used || v.Amount <= 0 {
		return ErrVoucherSpent
	}
	return ""
}

func (v *Voucher) apply(amount int64) {
	v.Amount -= amount
	if v.Amount == 0 {
		v.Used = true
	}
}
