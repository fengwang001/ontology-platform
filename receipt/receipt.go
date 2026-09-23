// Package receipt 定义投递收据：消息 id 加投递世代号。
//
// 消息每被重新投递一次，世代号递增，旧收据立即作废。
// 本包不依赖其他包。
package receipt

// Receipt 是某次投递的凭据。零值不是有效收据。
type Receipt struct {
	ID  int64 // 消息 id
	Gen int64 // 投递世代号，从 1 起，每次重新投递递增
}

// New 构造指定消息与世代的收据，仅供 queue 包调用。
func New(id, gen int64) Receipt { return Receipt{ID: id, Gen: gen} }

// Valid 报告收据是否携带正的世代号。
func (r Receipt) Valid() bool { return r.Gen > 0 }
