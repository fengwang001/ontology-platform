package vanload

// BatchLoadResult 批量装货成功结果。
type BatchLoadResult struct {
	Placements map[int]int // 货物编号 -> 所在分区
}

// BatchLoad 按给定次序逐件装货，全有或全无。
// 任一件失败则整批无变化，返回下标最小的失败件及原因。
// 批内编号重复（含与车上已装货物重复）报参数非法。
func (sys *System) BatchLoad(cargos []Cargo) (BatchLoadResult, error) {
	sys.mu.Lock()
	defer sys.mu.Unlock()

	input := batchInput(cargos)

	// 先做不改变状态的前置校验：参数合法性与编号唯一性。
	// 按下标递增判定，保证报告下标最小的问题件。
	seen := make(map[int]int, len(cargos)) // 编号 -> 首次出现下标
	for i, c := range cargos {
		if !c.Valid() {
			r := &Reject{Kind: KindInvalidArgument, FailedIndex: i}
			sys.emit("batch_load", input, rejectOutput(r),
				"下标 "+itoa(i)+" 字段必须为正整数且类别合法")
			return BatchLoadResult{}, r
		}
		if first, dup := seen[c.ID]; dup {
			r := &Reject{Kind: KindDuplicateInBatch, FailedIndex: i}
			sys.emit("batch_load", input, rejectOutput(r),
				"下标 "+itoa(i)+" 与下标 "+itoa(first)+" 编号重复")
			return BatchLoadResult{}, r
		}
		if _, onboard := sys.state.items[c.ID]; onboard {
			r := &Reject{Kind: KindDuplicateInBatch, FailedIndex: i}
			sys.emit("batch_load", input, rejectOutput(r),
				"下标 "+itoa(i)+" 编号已在车上")
			return BatchLoadResult{}, r
		}
		seen[c.ID] = i
	}

	// 在副本上逐件试装：每件看到前面各件放入后的状态。
	work := sys.state.clone()
	placements := make(map[int]int, len(cargos))
	for i, c := range cargos {
		if c.Stop <= work.arrivedStop {
			r := &Reject{Kind: KindStopPassed, FailedIndex: i}
			sys.emit("batch_load", input, rejectOutput(r),
				"下标 "+itoa(i)+" 停靠点 "+itoa(c.Stop)+" 已过")
			return BatchLoadResult{}, r
		}
		f := evaluateFeasibility(work, c)
		if f.chosen == 0 {
			r := &Reject{Kind: f.overall, FailedIndex: i, CompartmentReasons: f.reasons}
			sys.emit("batch_load", input, rejectOutput(r),
				"下标 "+itoa(i)+" "+reasonBasis(f))
			return BatchLoadResult{}, r // 副本被丢弃，真实状态无变化
		}
		work.states[f.chosen-1].addCargo(c)
		work.items[c.ID] = &itemRecord{cargo: c, compartment: f.chosen}
		placements[c.ID] = f.chosen
	}

	// 全部成功：一次性提交副本，保证并发查询只读到操作前或操作后的快照。
	sys.state = work
	res := BatchLoadResult{Placements: placements}
	sys.emit("batch_load", input, batchOutput(res), "全部货物放入可行最小编号分区，原子提交")
	return res, nil
}
