// Package loan 定义借券合约的基础结构。
package loan

type Contract struct {
	ID       int64
	Lender   []byte
	Borrower []byte
	Sym      []byte
	Qty      int64
}
