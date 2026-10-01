// Package fetch 实现多分区拉取响应组装器。
package fetch

import "sync"

// Assembler 在总字节预算 W 与单分区字节上限 P 下，
// 按轮转起点从 N 个分区环形读出消息并组装一次拉取响应。
type Assembler struct {
	mu         sync.Mutex
	n          int
	w          int
	p          int
	partitions [][]message
	consumePos []int
	rrStart    int
}

type message struct {
	size    int
	payload string
}

// New 构造组装器：n 为分区数，w 为单次响应总字节上限，p 为单分区字节上限。
func New(n, w, p int) (*Assembler, error) {
	if n < 1 {
		return nil, ErrInvalidPartitionCount
	}
	if w < 1 {
		return nil, ErrInvalidTotalBudget
	}
	if p < 1 {
		return nil, ErrInvalidPartitionLimit
	}
	return &Assembler{
		n:          n,
		w:          w,
		p:          p,
		partitions: make([][]message, n),
		consumePos: make([]int, n),
	}, nil
}

// Append 向指定分区追加一条消息，返回该消息在分区内从 0 起的位点。
// 分区越界与消息大小非法同时成立时，报分区越界。
func (a *Assembler) Append(partition, size int, payload string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if partition < 0 || partition >= a.n {
		return 0, ErrPartitionOutOfRange
	}
	if size < 1 {
		return 0, ErrInvalidMessageSize
	}
	offset := len(a.partitions[partition])
	a.partitions[partition] = append(a.partitions[partition], message{size: size, payload: payload})
	return offset, nil
}

// Fetch 执行一次拉取，返回组装好的响应。
//
// 从轮转起点起按环形顺序把全部 N 个分区各访问一次；访问分区时从其消费
// 位置起逐条取，取一条前先判定：本分区本次已取字节加该条大小大于 P，或
// 本响应已取总字节加该条大小大于 W，则该条不取且本分区结束（不跳过它去
// 取其后更小的消息）；恰等于上限可取。唯一例外：本响应已取总字节仍为 0
// 时，无视 P 与 W 取下一条（只此一条），其后照常判定。
func (a *Assembler) Fetch() (*Response, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	hasData := false
	for i := 0; i < a.n; i++ {
		if a.consumePos[i] < len(a.partitions[i]) {
			hasData = true
			break
		}
	}
	if !hasData {
		return nil, ErrNoData
	}

	resp := &Response{PartitionBytes: make(map[int]int)}
	lastTaken := -1
	for i := 0; i < a.n; i++ {
		part := (a.rrStart + i) % a.n
		partBytes := 0
		for a.consumePos[part] < len(a.partitions[part]) {
			msg := a.partitions[part][a.consumePos[part]]
			if resp.TotalBytes == 0 {
				// 首条例外：无视 P 与 W 取下一条，避免饿死。
				resp.FirstOverBudget = partBytes+msg.size > a.p || msg.size > a.w
			} else if partBytes+msg.size > a.p || resp.TotalBytes+msg.size > a.w {
				break
			}
			offset := a.consumePos[part]
			a.consumePos[part]++
			partBytes += msg.size
			resp.TotalBytes += msg.size
			resp.Records = append(resp.Records, Record{
				Partition: part,
				Offset:    offset,
				Size:      msg.size,
				Payload:   msg.payload,
			})
			lastTaken = part
		}
		if partBytes > 0 {
			resp.PartitionBytes[part] = partBytes
		}
	}
	a.rrStart = (lastTaken + 1) % a.n
	resp.NextStart = a.rrStart
	return resp, nil
}

// Snapshot 返回各分区消费位置与当前轮转起点的快照。
func (a *Assembler) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	snap := Snapshot{
		ConsumePositions: make([]int, a.n),
		Pending:          make([]int, a.n),
		RotationStart:    a.rrStart,
	}
	for i := 0; i < a.n; i++ {
		snap.ConsumePositions[i] = a.consumePos[i]
		snap.Pending[i] = len(a.partitions[i]) - a.consumePos[i]
	}
	return snap
}
