# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## spotdrain：Spot 回收预警下的任务排空与重调度

`spotdrain` 包实现抢占式（Spot）实例回收预警下的任务排空与重调度器。
构造参数为宽限期 `G`（1..10^6）、按需单价 `Pod`（1..10^6）与按需预算
`Bud`（0..10^12），任一越界则配置非法、整体拒绝（返回
`ErrInvalidConfig`，不创建调度器）。所有操作与查询均可并发调用，
内部以互斥锁串行化，结果等价于某个串行顺序；相同操作序列重放得到
完全相同的排空计划、返工量与放置结果。

### 排空计划的三类判定与累计规则（Notice）

`Notice(n, now)` 要求 `n` 为未被通知的 Spot 节点，截止时刻 `dl = now+G`。
对节点上每个运行任务（剩余量 `w-p`，未保存量 `u = p-cp`）：

- **自然完成**：`w-p <= G`（取等归入），任务可在宽限期内跑完；
- **已保存**：`u = 0` 的任务直接归入，不耗时也不占排序；其余 `u > 0`
  的任务按 `u` 降序（并列按 id 升序）逐个累计落点耗时 `ck`，累计值
  `<= G`（取等归入）则落检查点成功（令 `cp = p`）；
- **丢弃**：遇到第一个使累计值 `> G` 的任务即停止扫描，该任务与其后
  全部任务（包括更小的）都记为丢弃，不跳过去尝试更小的任务。

三个返回列表各自按任务 id 升序。节点被通知后不再接收新放置。

### 检查点与返工的口径

- `Report(id, p')` 要求 `p <= p' <= w`，令 `p = p'` 并推进
  `cp = max(cp, floor(p'/iv)*iv)`（只增不减）；`p' = w` 任务完成并释放槽位。
- `Expire(n, now)` 要求节点已通知且 `now >= dl`：节点移除，其上仍未完成
  的任务（含自然完成却未上报完成的）回到待放置，`rs` 加一、`p` 回退到
  `cp`；返回各任务的返工量（回退前的 `p - cp`，含 0 值项）。
  `TotalRework()` 恒等于各次 `Expire` 返工量之和。

### 放置次序与按需预算（Place）

待放置任务按 **(prio 降序, id 升序)** 逐个放置：

1. Spot 候选：未被通知回收且有空闲槽的 Spot 节点，取空闲槽最多者
   （并列取 id 小者）；`rs >= 2` 的任务没有 Spot 候选，直接走下一步；
2. 无 Spot 候选时，在有空闲槽的 OnDemand 节点中取 id 最小者，须成本
   `(w-cp)*Pod`（按放置时刻的 `cp` 计）不超过剩余预算，成功则扣除；
   预算不足则该任务继续待放置，**不阻塞**排在后面的任务。

### 校验顺序与被拒绝操作

- `AddNode` / `AddTask`：参数非法 → 已存在；
- `Notice`：参数非法（`now` 越界）→ 节点不存在 → 非 Spot 节点 → 已通知；
- `Expire`：参数非法 → 节点不存在 → 未通知 → 未到期（`now < dl`）；
- `Report`：任务不存在 → 任务未运行 → 进度非法。

每种操作只报第一个错误；被拒绝的操作不改变任何节点、任务、预算与槽位。

### 本地验证

```bash
# 单元测试（含题目示例逐字段复现、边界与错误顺序）
go test ./spotdrain/

# 竞态检测 + 2000 组随机节点/任务/事件序列与朴素模拟逐步对照
# （日志打印每组输入、输出与判定依据，可用 -run 指定 seed 查看）
go test -race ./spotdrain/
go test -v -run 'TestRandomAgainstModel/seed=7$' ./spotdrain/

# 重放一致性（相同操作序列两次执行结果完全相同）
go test -run TestReplayDeterminism ./spotdrain/
```

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
