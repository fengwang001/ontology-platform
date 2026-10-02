# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## Sink 两阶段提交事务名册

`ontology.Registry` 维护按检查点预提交的 sink 写入事务。构造参数为子任务数 `P`（1–64）与连续缺失容忍 `M`（1–1000）。

### 事务命名与世代

- 子任务 `s` 当前打开事务恒以 `(s, lb+1)` 命名，其中 `lb` 是最近一次成功 Barrier 的检查点号。
- Barrier 成功后 `lb` 前进，后续写入自动换到下一个检查点名；没有写入的子任务不创建事务。
- 首次创建的事务世代 `epoch=0`；同名位置已有旧事务时，新事务世代为旧世代加一。
- 恢复换代后，若在 `ABORTED` 名下重新写入，同样使用旧世代加一；若在旧生命周期遗留的 `OPEN` 或 `PREPARED` 名下写入，先中止遗留事务，再以加一世代重开。
- 每个名字下任一时刻至多有一个 `OPEN` 或 `PREPARED` 事务；`COMMITTED` 事务不再改变。

### 通知提交

- `Barrier(cp)` 只接受 `cp=lb+1`，并按 `s` 升序把当前生命周期中名为 `(s, cp)` 的 `OPEN` 事务转为 `PREPARED`。
- `Complete(c)` 提交当前生命周期中所有 `k<=c` 的 `PREPARED` 事务，清单按 `(k,s)` 排序。
- 通知允许跳号：`Complete(3)` 会一并提交检查点 1、2、3 中尚未提交的事务，然后将 `ln=3`。
- 旧生命周期的遗留 `PREPARED` 事务不会被后来的 `Complete` 提交。

### 恢复三步

`Restore(c, P')` 是单个原子步骤，依次完成：

1. **提交**：提交当前生命周期中所有 `k<=c` 的 `PREPARED` 事务，包含 `k>ln` 但 `k<=c`、检查点已完成而通知尚未送达的事务。
2. **清扫**：对 `s=0..max(P,P')-1`，从 `(s,c+1)` 开始逐个探测；命中任意生命周期的 `OPEN` 或 `PREPARED` 事务就中止并把连续缺失计数清零，无事务或遇到终态则计一次缺失。某个子任务连续缺失达到 `M` 后停止。
3. **换代**：设置 `P=P'`、`lb=ln=c`，并让 `life+1`。

缺失容忍是有限的恢复探测边界：它只能保证中止从 `c+1` 开始、在连续 `M` 次缺失窗口内遇到的非终态事务；窗口之后的遗留事务可能暂时保留，由后续写入同名位置时中止重开，或由以后的恢复继续处理。

### 记录守恒

任意时刻，所有被接受的写入记录总数满足：

```text
写入总数 = COMMITTED 记录 + ABORTED 记录 + OPEN 记录 + PREPARED 记录
```

`Stats()` 返回这四类记录总数以及恢复清扫累计探测次数。内部 `visited` 只统计 `Complete` 和恢复提交阶段扫描当前生命周期有序 PREPARED 前缀的条目数；一次提交访问量不超过本次提交事务数加一，不随历史已提交事务数量增长。

### 本地验证

```bash
go test ./...
go test -race -v ./ontology
go test ./ontology -run TestRandomSimulationAgainstNaiveModel -v -count=1
```

随机测试使用固定种子重放 2000 组操作序列，并在 `-v` 日志中打印每一步输入、实际输出、判定依据，以及与朴素模型的内部事务表和计数比对。

## 环境要求

- Go 1.26+（`go version` 确认）

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
