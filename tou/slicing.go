package tou

import (
	"math/big"
	"time"
)

// slice 是一个读数区间被所有边界切开后的最小计价片：
// [Start, End) 内版本、日类型、时段、月份均不变。
type slice struct {
	start    time.Time
	end      time.Time
	month    monthKey
	day      DayType
	startSec int
	price    int64
	billable bool
	energyWh int64
	// amountMilli 为该片金额（最小货币单位），不可计价片为 0。
	amountMilli int64
}

type aggKey struct {
	month    monthKey
	day      DayType
	startSec int
	price    int64
	billable bool
}

func dayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func nextDayStart(t time.Time) time.Time {
	return dayStart(t).AddDate(0, 0, 1)
}

func nextMonthStart(t time.Time) time.Time {
	y, m, _ := t.Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, t.Location()).AddDate(0, 1, 0)
}

// sliceInterval 把区间 [from, to)（总电量 totalWh，均匀发生）切成片。
// 边界包括：日界（也是月界来源）、电价版本生效时刻、时段边界。
// 分摊：每片 floor(total*dur/totalDur)，余量全部归最后一片。
func sliceInterval(from, to time.Time, totalWh int64, b *tariffBook, c *calendar) []slice {
	if !to.After(from) {
		return nil
	}
	totalDur := new(big.Int).SetInt64(int64(to.Sub(from) / time.Second))

	var out []slice
	for cur := from; cur.Before(to); {
		var end time.Time
		var billable bool
		var price int64
		var startSec int
		var day DayType

		v, vidx, ok := b.at(cur)
		dayEnd := nextDayStart(cur)
		monthEnd := nextMonthStart(cur)
		if !ok {
			// 不可计价片在每个版本生效时刻与日界处切断，使后续
			// 新登记的回溯版本只需重切其生效时刻覆盖到的区间。
			end = dayEnd
			if next := vidx + 1; next < len(b.versions) &&
				b.versions[next].EffectiveAt.After(cur) &&
				b.versions[next].EffectiveAt.Before(end) {
				end = b.versions[next].EffectiveAt
			}
			billable = false
			day = c.dayType(cur)
			startSec = cur.Hour()*3600 + cur.Minute()*60 + cur.Second()
			price = 0
		} else {
			day = c.dayType(cur)
			sched := v.Schedules[day]
			pidx := periodAt(sched, cur)
			startSec = sched.Periods[pidx].StartSec
			price = sched.Periods[pidx].Price
			periodEndAt := dayStart(cur).Add(time.Duration(periodEnd(sched, pidx)) * time.Second)
			end = periodEndAt
			billable = true
			// 版本切换：下一个版本生效时刻落在当前时段内时在此切断。
			if next := vidx + 1; next < len(b.versions) &&
				b.versions[next].EffectiveAt.After(cur) &&
				b.versions[next].EffectiveAt.Before(end) {
				end = b.versions[next].EffectiveAt
			}
		}
		// 月界是与日界并列的独立切点（多数月份两者只在 1 号重合）。
		if monthEnd.Before(end) {
			end = monthEnd
		}
		if end.After(to) {
			end = to
		}
		dur := int64(end.Sub(cur) / time.Second)
		energy := floorDiv(new(big.Int).Mul(big.NewInt(totalWh), big.NewInt(dur)), totalDur).Int64()
		out = append(out, slice{
			start: cur, end: end, month: monthKeyAt(cur), day: day,
			startSec: startSec, price: price, billable: billable, energyWh: energy,
		})
		cur = end
	}

	if len(out) > 0 {
		sum := int64(0)
		for _, s := range out {
			sum += s.energyWh
		}
		out[len(out)-1].energyWh += totalWh - sum
	}
	for i := range out {
		if out[i].billable {
			out[i].amountMilli = floorDiv(
				big.NewInt(out[i].energyWh*out[i].price), big.NewInt(priceScale)).Int64()
		}
	}
	return out
}

func floorDiv(a, b *big.Int) *big.Int {
	q := new(big.Int).Quo(a, b)
	r := new(big.Int).Rem(a, b)
	if r.Sign() < 0 {
		q.Sub(q, big.NewInt(1))
	}
	return q
}
