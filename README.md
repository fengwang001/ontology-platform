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

## 窗口结果抑制缓冲

`NewBuffer(S, G, E, policy)` 构造按窗口抑制结果的 `Buffer`：

- `S` 是窗口宽度，`Update(key, ts, value)` 的窗口起点为 `ws = floor(ts/S)×S`，窗口结束为 `end = ws + S`。
- `G` 是宽限期，条目的关闭时刻为 `end + G`；流时间 `ST` 初值为 `-1`，只能单调推进。
- 缓冲以 `(key, ws)` 去重，条目保存当前 `value` 与最后一次更新时间 `lastTs`；覆盖时即使新的 `ts` 较小，也替换这两个字段。
- 当 `end + G <= ST` 时，更新属于迟到数据：只增加迟到计数并返回 `StatusDiscarded`，不推进 `ST`，也不改变缓冲。

### Update 处理顺序

每次 `Update` 在单个互斥步骤内完成，外部不会观察到部分发出：

1. **参数校验**：空 key、越界 `ts/value` 直接返回 `ErrInvalidArgument`。
2. **迟到判定**：使用更新前的 `ST` 判断 `end + G <= ST`。
3. **推进流时间**：计算 `ST' = max(ST, ts)`，找出关闭时刻不大于 `ST'` 的既有条目。
4. **容量判定**：关闭条目先腾出名额；若剩余打开条目加上本次新条目数超过 `E`，执行满时策略。
5. **生效**：先按 `(end, key)` 升序发出关闭条目并标记 `Final`，再按同序发出容量策略选中的条目并标记 `Early`，最后写入本次条目并设置 `ST = ST'`。

### 满时策略

- `EmitEarly`：早发 `rest + needNew - E` 个打开条目。早发只从既有条目中选择，按 `(end, key)` 升序取最小者；本次更新自身永不早发。早发会立即移出缓冲，因此之后同一 `(key, ws)` 再次到达时是新条目，可重新缓冲；到达关闭时刻后会再以 `Final` 发出。
- `Shutdown`：容量不足时拒绝本次更新并返回 `ErrBufferFull`。关闭集合不会提前发出，`ST`、缓冲和迟到计数均保持不变；若本次更新推进的 `ST'` 已使旧条目关闭，则这些名额会先释放，不再误拒。

### Tick 与查询

`Tick(t)` 先检查参数，再判断流时间是否回退：

- `t < ST` 返回 `ErrStreamTimeRollback`，不改变状态。
- `t == ST` 是空操作，返回空切片。
- `t > ST` 发出关闭时刻不大于 `t` 的全部条目（`Final`，按 `(end, key)` 升序），然后设置 `ST = t`。

查询接口包括：

- `Buffered()`：按 `(end, key)` 升序返回当前缓冲快照。
- `StreamTime()`：返回当前流时间。
- `Late()`：返回迟到丢弃计数。

内部使用按 `(end, key)` 有序的平衡树维护缓冲，非导出 `peeks` 计数器只统计为定位关闭集合和早发边界所做的逻辑探查。每次操作的增量不超过本次发出条目数加 2，并且与缓冲总大小无关。

### 本地验证

```bash
# 全量测试
GOCACHE=/tmp/go-cache go test ./...

# 竞态检测
GOCACHE=/tmp/go-cache go test -race ./...

# 只运行 2000 组随机操作与朴素全表排序模型的对照
GOCACHE=/tmp/go-cache go test -run TestRandomizedNaiveComparison -v ./...

go vet ./...
gofmt -l .
```

随机测试使用固定 PCG 种子，逐组重放 `Update/Tick`，对照朴素模型的发出序列、缓冲、流时间和迟到计数；`-v` 模式会逐组打印输入、输出和判定依据。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
