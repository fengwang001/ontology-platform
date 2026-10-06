package reins

// Cession 是保单登记时确定的分出结构，以保额为基准的金额表示。
type Cession struct {
	QuotaShare int64
	Surplus    int64
	Net        int64
}

type policy struct {
	no         string
	sumInsured int64
	startSec   int64
	endSec     int64
	ces        Cession
}

const secondsPerDay = 86400

// computeCession 先成数分出，剩余部分在自留额以内归本公司，
// 超出自留额的部分分给溢额合约但不超过 自留额*线数，再超出则拒绝。
func computeCession(t Treaty, sumInsured int64) (Cession, error) {
	qs := sumInsured * int64(t.QuotaSharePercent) / 100
	rem := sumInsured - qs
	var sur int64
	if rem > t.SurplusRetention {
		sur = rem - t.SurplusRetention
		if sur > t.SurplusRetention*int64(t.SurplusLines) {
			return Cession{}, &Error{CodeCapacityExceeded, "超出承保能力"}
		}
	}
	return Cession{QuotaShare: qs, Surplus: sur, Net: sumInsured - qs - sur}, nil
}

// covers 承保区间 [startDay, endDay) 以天计，事故时刻为秒。
func (p *policy) covers(t int64) bool {
	return t >= p.startSec && t < p.endSec
}
