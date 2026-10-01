// Package netting 实现多边净额轧差清算器。
//
// 清算器按周期工作：Register 登记参与方（跨周期保留），Submit 向当前
// 周期登记应付义务，Close 原子地清算当前周期并开启新周期。清算采用
// 净借记上限违约级联规则，并由最终净头寸生成确定性的划付指令。
package netting

import (
	"sort"
	"sync"
)

const (
	// MaxCap 是参与方净借记上限的最大值。
	MaxCap = 1_000_000_000_000_000
	// MaxAmount 是单条义务金额的最大值。
	MaxAmount = 1_000_000_000_000
	// MaxObligationsPerCycle 是每个周期义务数量的上限。
	MaxObligationsPerCycle = 100_000
)

// Obligation 是一条应付义务：From 应付 To 金额 Amount。
type Obligation struct {
	OID    string
	From   string
	To     string
	Amount int64
}

// Instruction 是一条最终划付指令：Payer 向 Payee 划付 Amount。
type Instruction struct {
	Payer  string
	Payee  string
	Amount int64
}

// Position 是单个参与方在最终有效义务上的净头寸。
type Position struct {
	Party string
	Net   int64
}

// CloseResult 是 Close 的清算结果。
type CloseResult struct {
	// Cycle 是本次关闭的周期号，从 1 起递增。
	Cycle int64
	// Defaulters 按认定轮次、轮内 id 字节序升序记录全部违约方。
	Defaulters []string
	// RevokedOIDs 是被撤销义务的 oid 清单，按字节序升序。
	RevokedOIDs []string
	// Positions 是全体已登记参与方的最终净头寸，按 id 升序。
	Positions []Position
	// Instructions 是最终划付指令，按产生次序排列。
	Instructions []Instruction
}

// Engine 是并发安全的多边净额轧差清算器。
type Engine struct {
	mu sync.Mutex

	caps        map[string]int64
	cycle       int64
	obligations []Obligation
	oidSet      map[string]struct{}
}

// NewEngine 创建清算器，当前周期号为 1。
func NewEngine() *Engine {
	return &Engine{
		caps:   make(map[string]int64),
		cycle:  1,
		oidSet: make(map[string]struct{}),
	}
}

// Register 登记参与方。id 必须为非空字节串，cap 必须在 [0, 10^15]。
func (e *Engine) Register(id string, cap int64) error {
	if id == "" || cap < 0 || cap > MaxCap {
		return ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.caps[id]; ok {
		return ErrDuplicateParty
	}
	e.caps[id] = cap
	return nil
}

// Submit 向当前周期登记义务。
func (e *Engine) Submit(oid, from, to string, amount int64) error {
	if oid == "" || amount < 1 || amount > MaxAmount {
		return ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.oidSet[oid]; ok {
		return ErrDuplicateOID
	}
	if _, ok := e.caps[from]; !ok {
		return ErrUnknownParty
	}
	if _, ok := e.caps[to]; !ok {
		return ErrUnknownParty
	}
	if from == to {
		return ErrSelfCounterparty
	}
	if len(e.obligations) >= MaxObligationsPerCycle {
		return ErrCycleFull
	}
	e.obligations = append(e.obligations, Obligation{OID: oid, From: from, To: to, Amount: amount})
	e.oidSet[oid] = struct{}{}
	return nil
}

// Close 原子地清算当前周期并开启新周期。
func (e *Engine) Close() CloseResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	result := CloseResult{
		Cycle:        e.cycle,
		Defaulters:   []string{},
		RevokedOIDs:  []string{},
		Positions:    []Position{},
		Instructions: []Instruction{},
	}

	obligations := e.obligations
	alive := make([]bool, len(obligations))
	for i := range obligations {
		alive[i] = true
	}
	defaulted := make(map[string]bool)
	revokedSet := make(map[string]struct{})

	// 第一步/第二步：逐轮同时认定违约方并撤销其全部关联义务。
	for {
		net := computeNet(e.caps, obligations, alive, defaulted)
		var roundDefaulters []string
		for party, position := range net {
			if position < 0 && -position > e.caps[party] {
				roundDefaulters = append(roundDefaulters, party)
			}
		}
		if len(roundDefaulters) == 0 {
			break
		}
		sort.Strings(roundDefaulters)
		for _, party := range roundDefaulters {
			defaulted[party] = true
			result.Defaulters = append(result.Defaulters, party)
		}
		for i, ob := range obligations {
			if alive[i] && (defaulted[ob.From] || defaulted[ob.To]) {
				alive[i] = false
				revokedSet[ob.OID] = struct{}{}
			}
		}
	}

	// 第三步：用最终有效义务得到各方净头寸。
	finalNet := computeNet(e.caps, obligations, alive, defaulted)

	parties := make([]string, 0, len(e.caps))
	for party := range e.caps {
		parties = append(parties, party)
	}
	sort.Strings(parties)
	for _, party := range parties {
		result.Positions = append(result.Positions, Position{Party: party, Net: finalNet[party]})
	}

	for oid := range revokedSet {
		result.RevokedOIDs = append(result.RevokedOIDs, oid)
	}
	sort.Strings(result.RevokedOIDs)

	result.Instructions = settle(finalNet)

	// 开启新周期：义务清空，参与方登记保留。
	e.obligations = nil
	e.oidSet = make(map[string]struct{})
	e.cycle++

	return result
}

// computeNet 汇总当前仍有效且不涉及已违约方的义务，返回全体已登记
// 参与方的净头寸（应收之和减应付之和）；无义务的参与方净头寸为 0。
func computeNet(caps map[string]int64, obligations []Obligation, alive []bool, defaulted map[string]bool) map[string]int64 {
	net := make(map[string]int64, len(caps))
	for party := range caps {
		net[party] = 0
	}
	for i, ob := range obligations {
		if !alive[i] || defaulted[ob.From] || defaulted[ob.To] {
			continue
		}
		net[ob.From] -= ob.Amount
		net[ob.To] += ob.Amount
	}
	return net
}

type sideEntry struct {
	party  string
	remain int64
}

// settle 按金额绝对值降序、id 升序排列付方与收方，反复以队首撮合，
// 剩余为 0 出队、不为 0 则保留在队首且不重新排序。
func settle(net map[string]int64) []Instruction {
	var payers, receivers []sideEntry
	for party, position := range net {
		switch {
		case position < 0:
			payers = append(payers, sideEntry{party: party, remain: -position})
		case position > 0:
			receivers = append(receivers, sideEntry{party: party, remain: position})
		}
	}
	sortSide := func(entries []sideEntry) {
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].remain != entries[j].remain {
				return entries[i].remain > entries[j].remain
			}
			return entries[i].party < entries[j].party
		})
	}
	sortSide(payers)
	sortSide(receivers)

	instructions := []Instruction{}
	for len(payers) > 0 && len(receivers) > 0 {
		amount := payers[0].remain
		if receivers[0].remain < amount {
			amount = receivers[0].remain
		}
		instructions = append(instructions, Instruction{
			Payer:  payers[0].party,
			Payee:  receivers[0].party,
			Amount: amount,
		})
		payers[0].remain -= amount
		receivers[0].remain -= amount
		if payers[0].remain == 0 {
			payers = payers[1:]
		}
		if len(receivers) > 0 && receivers[0].remain == 0 {
			receivers = receivers[1:]
		}
	}
	return instructions
}
