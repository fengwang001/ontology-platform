package settlement

import (
	"fmt"
	"math/rand"
)

// genLoadedDataset produces a merchant config and a set of transactions with
// random days across [0, horizon], all added before any settlement. It is used
// to compare one catch-up settlement against per-day settlements under
// identical input data.
func genLoadedDataset(seed int64) (MerchantConfig, []Transaction) {
	rng := rand.New(rand.NewSource(seed))
	cfg := MerchantConfig{
		SettleDelayN:    1 + rng.Intn(3),
		ReserveBps:      []int{0, 100, 500, 1000, 2500, 10000}[rng.Intn(6)],
		ReserveHorizonH: 1 + rng.Intn(4),
	}
	n := 120 + rng.Intn(80)
	txns := make([]Transaction, 0, n)
	for i := 0; i < n; i++ {
		txns = append(txns, Transaction{
			ID:     fmt.Sprintf("x%d", i),
			Day:    Day(rng.Intn(26)), // <= horizon-N so everything processes
			Amount: Amount(rng.Intn(201) - 140),
		})
	}
	return cfg, txns
}
