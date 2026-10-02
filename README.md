# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

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

## 进度前沿追踪器

根包提供带路径摘要与因果校验的线程安全进度前沿追踪器：

- `NewTracker(n, edges, sources)` 校验位置数、边、延迟和源集合，并用 Floyd-Warshall 计算路径摘要 `d(u,v)`。
- `d⁺(u,v)` 是至少含一条边的最小延迟和；`u != v` 时 `d(u,v)=d⁺(u,v)`，而 `d(v,v)=0`，因此位置自己持有的条目直接计入自身前沿，不需要绕环。
- 零延迟有向环（含零延迟自环）及所有非法配置整体拒绝。
- 前沿 `F(v)=min(t+d(p,v))`，最小值只取当前计数大于 0 的 `(p,t)`；不可达或无可达条目时对外返回 `-1`。
- 内部为每个位置维护活跃时间戳最小堆，批后只通过每位置当前最小活跃时间戳重算前沿，不随每位置历史时间戳数量增长而扫描整个账本。

### 批更新

`Update(batch)` 先把相同 `(位置, 时间戳)` 的增量合并为净增量。所有三元组先完成原始参数校验，随后按顺序执行：

1. 参数非法：批大小、位置、时间戳、零增量或超范围增量，以及应用后计数超过 `10^12`。
2. 计数为负：按 `(位置, 时间戳)` 升序报告第一个会变为负数的净增量键。
3. 因果违反：仅对净增量为正且位置不在源集合中的键检查，要求批生效前 `F(q) <= t`，按键升序报告第一个。

只有全部检查通过才写入账本并将 `ver` 加一；拒绝不会产生部分写入。因果判断使用批生效前快照，所以同批消费旧项并生产新项时，只要旧前沿能为新项辩护即可接受。源位置免除因果检查。

变化清单按位置升序列出前沿确实变化的位置，每项给出变化前值和变化后值；无穷前沿统一编码为 `-1`。

### 查询

- `Frontier(v)` 返回单个位置的前沿。
- `Frontiers()` 返回 `(ver, 全体前沿)` 的一致快照。
- `Complete(v,t)` 仅当 `F(v)=-1` 或 `F(v)>t` 时为真；`t == F(v)` 时为假。
- `EntryVisits()` 暴露非导出计数器的测试/观测口径，统计前沿重算与因果校验中的账本条目读取。

所有更新在写锁下原子完成，查询在读锁下读取一致快照；并发结果等价于某个串行顺序。

### 本地验证

```bash
go test ./...
go test -race -v ./...
go test -run TestRandomBatchSequencesMatchNaive -v
```

随机对照测试使用固定种子，驱动 2000 组配置和批序列，与每批重新执行 Floyd-Warshall、全账本扫描的朴素模拟器逐项比较版本、账本、前沿、变化清单和拒绝原因；`-v` 日志包含输入、输出与判定依据。
