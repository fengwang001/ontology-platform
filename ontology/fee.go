package ontology

const maxBillingAmount int64 = 100_000_000_000_000
const basisPointDenominator int64 = 10_000

func (b *CumulativeBilling) uncappedFee(cumulative int64) int64 {
	var fee int64
	lastThreshold := int64(0)
	for segment, threshold := range b.thresholds {
		if cumulative <= threshold {
			if cumulative > lastThreshold {
				fee += (cumulative - lastThreshold) * b.rates[segment] / basisPointDenominator
			}
			return fee
		}
		fee += (threshold - lastThreshold) * b.rates[segment] / basisPointDenominator
		lastThreshold = threshold
	}
	if cumulative > lastThreshold {
		fee += (cumulative - lastThreshold) * b.rates[len(b.thresholds)] / basisPointDenominator
	}
	return fee
}

func (b *CumulativeBilling) cumulativeFee(cumulative int64) int64 {
	fee := b.uncappedFee(cumulative)
	if fee > b.capFee {
		return b.capFee
	}
	return fee
}
