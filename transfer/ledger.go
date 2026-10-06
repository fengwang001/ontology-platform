package transfer

import "math/big"

func addQuantity(left, right Quantity) (Quantity, bool) {
	if left > ^Quantity(0)-right {
		return 0, false
	}
	return left + right, true
}

type stockKey struct {
	warehouse Warehouse
	product   Product
}

type transitKey struct {
	order     OrderID
	lineIndex int
	product   Product
}

type ledger struct {
	stock          map[stockKey]StockView
	inTransitLines map[transitKey]Quantity
	shortage       map[Product]*big.Int
	surplus        map[Product]*big.Int
	initial        map[Product]*big.Int
}

func newLedger(initial map[Warehouse]map[Product]Quantity) *ledger {
	l := &ledger{
		stock:          map[stockKey]StockView{},
		inTransitLines: map[transitKey]Quantity{},
		shortage:       map[Product]*big.Int{},
		surplus:        map[Product]*big.Int{},
		initial:        map[Product]*big.Int{},
	}
	for warehouse, products := range initial {
		for product, quantity := range products {
			key := stockKey{warehouse: warehouse, product: product}
			l.stock[key] = StockView{Available: quantity}
			l.initial[product] = new(big.Int).Add(l.bigValue(l.initial, product), new(big.Int).SetUint64(uint64(quantity)))
		}
	}
	return l
}

func (l *ledger) stockAt(warehouse Warehouse, product Product) StockView {
	return l.stock[stockKey{warehouse: warehouse, product: product}]
}

func (l *ledger) available(warehouse Warehouse, product Product) Quantity {
	return l.stockAt(warehouse, product).Available
}

func (l *ledger) canAddAvailable(warehouse Warehouse, product Product, quantity Quantity) bool {
	_, ok := addQuantity(l.stockAt(warehouse, product).Available, quantity)
	return ok
}

func (l *ledger) freeze(warehouse Warehouse, product Product, quantity Quantity) {
	key := stockKey{warehouse: warehouse, product: product}
	stock := l.stock[key]
	stock.Available -= quantity
	frozen, ok := addQuantity(stock.Frozen, quantity)
	if !ok {
		panic("frozen quantity overflow")
	}
	stock.Frozen = frozen
	l.stock[key] = stock
}

func (l *ledger) release(warehouse Warehouse, product Product, quantity Quantity) {
	key := stockKey{warehouse: warehouse, product: product}
	stock := l.stock[key]
	stock.Frozen -= quantity
	available, ok := addQuantity(stock.Available, quantity)
	if !ok {
		panic("available quantity overflow")
	}
	stock.Available = available
	l.stock[key] = stock
}

func (l *ledger) ship(order OrderID, lineIndex int, warehouse Warehouse, product Product, quantity Quantity) {
	key := stockKey{warehouse: warehouse, product: product}
	stock := l.stock[key]
	stock.Frozen -= quantity
	l.stock[key] = stock
	l.inTransitLines[transitKey{order: order, lineIndex: lineIndex, product: product}] = quantity
}

func (l *ledger) receive(order OrderID, lineIndex int, warehouse Warehouse, product Product, quantity Quantity) {
	key := stockKey{warehouse: warehouse, product: product}
	stock := l.stock[key]
	available, ok := addQuantity(stock.Available, quantity)
	if !ok {
		panic("destination available quantity overflow")
	}
	stock.Available = available
	l.stock[key] = stock

	lineKey := transitKey{order: order, lineIndex: lineIndex, product: product}
	inTransit := l.inTransitLines[lineKey]
	if quantity <= inTransit {
		l.inTransitLines[lineKey] = inTransit - quantity
		return
	}

	extra := quantity - inTransit
	delete(l.inTransitLines, lineKey)
	l.surplus[product] = new(big.Int).Add(l.bigValue(l.surplus, product), new(big.Int).SetUint64(uint64(extra)))
}

func (l *ledger) closeLine(order OrderID, lineIndex int, product Product, shipped Quantity, received Quantity) {
	if shipped > received {
		delete(l.inTransitLines, transitKey{order: order, lineIndex: lineIndex, product: product})
		l.shortage[product] = new(big.Int).Add(l.bigValue(l.shortage, product), new(big.Int).SetUint64(uint64(shipped-received)))
	}
}

func (l *ledger) recover(product Product, quantity Quantity) {
	l.shortage[product].Sub(l.shortage[product], new(big.Int).SetUint64(uint64(quantity)))
}

func (l *ledger) bigValue(values map[Product]*big.Int, product Product) *big.Int {
	value := values[product]
	if value == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(value)
}

func (l *ledger) verify() bool {
	products := map[Product]struct{}{}
	for product := range l.initial {
		products[product] = struct{}{}
	}
	for key := range l.stock {
		products[key.product] = struct{}{}
	}
	for key := range l.inTransitLines {
		products[key.product] = struct{}{}
	}
	for product := range l.shortage {
		products[product] = struct{}{}
	}
	for product := range l.surplus {
		products[product] = struct{}{}
	}

	inTransitByProduct := map[Product]*big.Int{}
	for key, quantity := range l.inTransitLines {
		current := inTransitByProduct[key.product]
		if current == nil {
			current = new(big.Int)
		}
		inTransitByProduct[key.product] = new(big.Int).Add(current, new(big.Int).SetUint64(uint64(quantity)))
	}

	for product := range products {
		left := new(big.Int)
		for key, stock := range l.stock {
			if key.product != product {
				continue
			}
			left.Add(left, new(big.Int).SetUint64(uint64(stock.Available)))
			left.Add(left, new(big.Int).SetUint64(uint64(stock.Frozen)))
		}
		if inTransit := inTransitByProduct[product]; inTransit != nil {
			left.Add(left, inTransit)
		}
		left.Add(left, l.bigValue(l.shortage, product))

		right := l.bigValue(l.initial, product)
		right.Add(right, l.bigValue(l.surplus, product))
		if left.Cmp(right) != 0 {
			return false
		}
	}
	return true
}
