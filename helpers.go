package ontology

import "math/big"

func validTerms(terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	for _, term := range terms {
		if term == "" {
			return false
		}
	}
	return true
}

func uniqueTerms(terms []string) []string {
	seen := make(map[string]struct{}, len(terms))
	result := make([]string, 0, len(terms))
	for _, term := range terms {
		if _, ok := seen[term]; !ok {
			seen[term] = struct{}{}
			result = append(result, term)
		}
	}
	return result
}

func termFrequencies(terms []string) map[string]int {
	result := make(map[string]int)
	for _, term := range terms {
		result[term]++
	}
	return result
}

func termScore(tf, df, dl int, stat globalStat) *big.Rat {
	idf := big.NewRat(int64(2*(stat.n-df)+1), int64(2*df+1))
	dlRatio := big.NewRat(int64(dl*stat.n), int64(stat.l))
	lengthPart := new(big.Rat).Add(
		big.NewRat(1, 4),
		new(big.Rat).Mul(big.NewRat(3, 4), dlRatio),
	)
	denominator := new(big.Rat).Add(
		big.NewRat(int64(tf), 1),
		new(big.Rat).Mul(big.NewRat(3, 2), lengthPart),
	)
	numerator := new(big.Rat).Mul(idf, big.NewRat(int64(tf*5), 2))
	return new(big.Rat).Quo(numerator, denominator)
}

func ratString(value *big.Rat) string {
	return value.Num().String() + "/" + value.Denom().String()
}
