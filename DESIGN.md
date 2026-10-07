# 微电网储能调度控制器 — 设计说明

## 模块划分（microgrid 包，8 个 .go 文件）
- `types.go`：Action/Mode/Config/ErrKind/RejectError；错误类别声明顺序即固定拒绝次序。
- `forecast.go`：关键负荷预测与本地发电盈余；备用求和按 horizon 逐点查表。
- `plan.go`：纯函数（折损折算、符号电量、参数校验）与已接受计划簿（map + 升序键）。
- `controller.go`：推演引擎 checkSlot/replay、后缀撤销 revalidate、全部公开操作。
- `controller_test.go` 边界单测；`model_test.go` 朴素模型 + 随机差分；`bench_test.go` 性能基准。

## 关键取舍
- 单互斥锁串行化全部操作，天然满足"等价于某个串行顺序"；无锁化被放弃（收益低、难证正确）。
- 撤销统一为"后缀截断"：任一触发源（预测更新/偏差/切孤岛/锁定）都复用同一 revalidate。
- 折损用千分比整数（`amount*(1000-p)/1000` 向下取整），放弃浮点，保证重放逐位一致。
- 推演只依赖 map + 排序键，不引入树/堆；已执行时隙的预测与计划即时删除。
- 备用判定 O(horizon)：horizon 是配置常数，放弃 Fenwick/线段树（复杂度已达标）。
- 计划时隙的预测缺失按"预测缺失"报错；备用求和与孤岛上限中缺失预测按 0 计。
- 维护锁定的孤岛豁免同时作用于新计划与已接受计划；切回并网按规格不撤销
  （孤岛豁免时隙可能因此暂存，留待下一次重推演触发时处理）。
- 实际登记只校验越界（不校验模式/上限），被拒绝的登记不改变任何状态、时隙不推进。

## 性能论证（可验证）
- RecordActual 只触碰未执行时隙：`ReplaySteps` 计数器在
  `TestRecordActualCostIndependentOfHistory` 中断言步数等于剩余已接受时隙数，
  与 200 个历史时隙无关；`BenchmarkRecordActual` 显示 history=0/1000/10000 耗时持平。
- 备用判定与预测总长无关：`ForecastQueries` 计数器在
  `TestReserveCheckCostIndependentOfForecastSize` 中断言恰为 horizon×推演时隙数。

## 本地验证
- `go test ./microgrid/`：边界单测（上下限取等、备用恰够、折损取整、重叠保留失效、
  后缀撤销、容忍量取等、孤岛取等、锁定取等与孤岛例外、拒绝次序与首时隙报告）。
- `go test -race ./microgrid/`：并发冒烟 + 200 种子 × 300 步随机差分对照朴素模型，
  逐步比对错误类别/时隙、撤销序列、荷电、计划、预测、吞吐（`-v` 打印每条输入输出）。
- `go test -run '^$' -bench . ./microgrid/`；`gofmt -l .`、`go vet ./...`。
