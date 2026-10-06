package chargeback

import "sort"

type dupKey struct {
	card     string
	merchant string
	amount   int64
}

// registry stores transactions and per-transaction / per-basis case indexes.
// Occupancy, merchant-win and basis-lock questions are answered by scanning
// only the cases of the relevant transaction or basis transaction, so cost
// never grows with the global number of cases or transactions.
type registry struct {
	txn        map[string]*Transaction
	byDupKey   map[dupKey][]*Transaction
	txnCases   map[string][]string
	basisFor   map[string][]string
	merchCases map[string][]string
	openCases  []string // cases without an explicit terminating operation
	merchOpen  map[string][]string
}

func newRegistry() *registry {
	return &registry{
		txn:        map[string]*Transaction{},
		byDupKey:   map[dupKey][]*Transaction{},
		txnCases:   map[string][]string{},
		basisFor:   map[string][]string{},
		merchCases: map[string][]string{},
		merchOpen:  map[string][]string{},
		openCases:  nil,
	}
}

func (r *registry) add(txn *Transaction) {
	r.txn[txn.ID] = txn
	k := dupKey{txn.CardID, txn.MerchantID, txn.Amount}
	list := r.byDupKey[k]
	list = append(list, txn)
	sort.Slice(list, func(i, j int) bool { return list[i].SettlementDay < list[j].SettlementDay })
	r.byDupKey[k] = list
}

// findBasis returns an earlier settled transaction with the same card,
// merchant and amount within rangeDays (exact range day counts).
func (r *registry) findBasis(t *Transaction, rangeDays int) *Transaction {
	k := dupKey{t.CardID, t.MerchantID, t.Amount}
	for _, b := range r.byDupKey[k] {
		if b.ID == t.ID {
			continue
		}
		diff := t.SettlementDay - b.SettlementDay
		if diff > 0 && diff <= rangeDays {
			return b
		}
	}
	return nil
}

func (r *registry) get(txnID string) *Transaction { return r.txn[txnID] }

func (r *registry) addCase(txnID, merchantID, basisTxnID, caseID string) {
	r.txnCases[txnID] = append(r.txnCases[txnID], caseID)
	r.merchCases[merchantID] = append(r.merchCases[merchantID], caseID)
	if basisTxnID != "" {
		r.basisFor[basisTxnID] = append(r.basisFor[basisTxnID], caseID)
	}
}

// casesOf returns cases filed against a transaction.
func (r *registry) casesOf(txnID string) []string { return r.txnCases[txnID] }

// casesOnBasis returns duplicate cases that cited basisTxnID.
func (r *registry) casesOnBasis(basisTxnID string) []string {
	return r.basisFor[basisTxnID]
}

// casesOfMerchant returns cases affecting a merchant.
func (r *registry) casesOfMerchant(merchantID string) []string {
	return r.merchCases[merchantID]
}

func (r *registry) openList() []string { return r.openCases }

func (r *registry) addOpen(caseID string) {
	r.openCases = append(r.openCases, caseID)
}

func (r *registry) removeOpen(caseID string) {
	out := r.openCases[:0]
	for _, id := range r.openCases {
		if id != caseID {
			out = append(out, id)
		}
	}
	r.openCases = out
}

func (r *registry) addMerchantOpen(merchantID, caseID string) {
	r.merchOpen[merchantID] = append(r.merchOpen[merchantID], caseID)
}

func (r *registry) removeMerchantOpen(merchantID, caseID string) {
	out := r.merchOpen[merchantID][:0]
	for _, id := range r.merchOpen[merchantID] {
		if id != caseID {
			out = append(out, id)
		}
	}
	r.merchOpen[merchantID] = out
}

func (r *registry) openCasesOfMerchant(merchantID string) []string {
	return r.merchOpen[merchantID]
}
