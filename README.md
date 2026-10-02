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

## 离群点摘除器

`OutlierEjector` 位于根包，构造参数为：

- `N`：主机数量，主机编号为 `0..N-1`
- `K`：连续失败次数阈值
- `B`：基础摘除时长
- `Cap`：摘除时长上限
- `P`：最大摘除百分比，范围 `0..100`
- `Wn`：结果窗口长度，范围 `1..64`
- `Q`：窗口失败率阈值百分比，范围 `1..100`

### 摘除条件

失败上报先追加到滑动结果窗口并增加连续失败数 `c`，以下任一条件成立即尝试摘除：

- 连续失败：`c >= K`
- 窗口失败率：窗口恰好有 `Wn` 条已记录结果，且 `f*100 >= Q*Wn`

比较全部使用整数乘法，不做除法取整。比例检查同样用整数比较：当前摘除主机数为 `E` 时，仅当 `(E+1)*100 <= P*N` 才允许摘除，因此摘除数量不超过 `⌊P*N/100⌋`。被比例上限挡下时，本次失败仍保留在 `c` 和窗口中，后续每次失败都会重新尝试。

成功上报会清零 `c`、向窗口追加成功，并把摘除次数 `e` 减一（最低为 0）。摘除中主机的上报完全忽略，`c`、`e`、摘除截止时刻 `u` 与窗口都不变。

### 摘除时长与恢复

第 `e` 次摘除成功时先执行 `e++`，摘除时长为：

```text
min(Cap, B*e)
u = now + min(Cap, B*e)
```

主机在 `u > now` 时处于摘除状态；当 `now == u` 时已经恢复，无需额外操作，恢复本身不改变 `c`、`e` 或窗口。摘除成功瞬间会清零 `c` 并清空窗口。

### 乱序批量上报

`ReportBatch(events)` 原子处理一批 `(host, ok, now)`：

1. 按输入顺序预检事件，只返回第一个错误，先检查主机编号，再检查时间。
2. 再检查批内最小 `now` 是否小于已接受的最大 `now`。
3. 任一预检失败时，整批不改变任何主机状态和最大时间。
4. 预检通过后按 `now` 升序稳定排序；相同 `now` 保持输入顺序。
5. 按排序后的顺序执行 `Report` 规则，但返回值仍按原输入顺序排列。
6. 空批返回空结果且不改变状态。

`Report`、`ReportBatch`、`Ejected` 和 `Healthy` 都受互斥保护，可并发调用。查询会校验时间但不推进最大时间；非法主机、非法时间和时钟回退分别返回 `ErrInvalidHost`、`ErrInvalidTime`、`ErrClockRollback`。

### 本地验证

```bash
go test -v ./...
go test -race ./...
go vet ./...
```

随机对照测试 `TestRandomSequencesAgainstNaiveModel` 会运行 2000 组随机单报与乱序批序列，日志逐条打印输入、返回、触发/挡下依据、各主机状态和最大时间，并与按规则独立实现的朴素模拟器完全对齐。
