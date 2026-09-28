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

## 按时间戳查位点（`timelog` 包）

`timelog` 包在时间戳**不保证单调**的只追加日志上，提供“给定时间，定位应开始重读的位点”的能力，查询结果与逐条朴素扫描完全一致且可复现。

### 时间索引的建立

- 追加每条消息时，仅当其时间戳**严格大于此前见过的最大时间戳**时，才把 `(时间戳, 位点)` 记入时间索引；时间戳相等或回退（含回退后新高相等）一律不记。
- 因此索引中的时间戳严格递增、位点严格递增，可直接二分查找；索引项数量只随严格新高增长，与消息总数解耦。
- 位点即消息在日志中的下标（从 0 开始）；日志结束位点等于已有消息条数，即下一条消息将占据的位点。

### 查询语义

`SeekByTimestamp(ts)` 返回**位点最小**的、时间戳不小于 `ts` 的消息位点：

- 在索引上二分找到首个 `index[i].timestamp >= ts`，其位点即答案。查询只经索引定位，不逐条扫描消息。
- 命中时返回该位点并置 `Found=true`。
- 没有任何消息满足条件（例如目标时间大于全局最大时间戳，或日志为空）时，返回日志结束位点并置 `Found=false`，调用方可从该位点继续等待/重读新数据。
- 对同一快照，查询结果对 `ts` 单调不减；已存在的首个命中位点不会因为后续追加（即使追加更小/相等时间戳）而改变。

### 边界与非法输入

所有拒绝均为**整体拒绝、失败不留痕**：被拒操作不会改变任何已有消息、日志结束位点或索引。错误通过 `*timelog.Error` 返回，三类原因互不相同、可用 `errors.Is` 区分：

| 类别 | 哨兵 | 触发场景 |
| --- | --- | --- |
| `KindInvalidArgument` | `ErrInvalidArgument` | 空批次追加、`New` 中 `MaxEntries < 0` |
| `KindNegativeTimestamp` | `ErrNegativeTimestamp` | 追加批次中任一消息时间戳为负；查询时间为负 |
| `KindCapacityExceeded` | `ErrCapacityExceeded` | 追加成功后总条数将超过 `MaxEntries`（整批不落盘） |

其他边界：空日志上任意非负查询都返回位点 0、`Found=false`；`ts=0` 合法；`MaxEntries=0` 表示不限容量。

### 并发

`Append` 与 `SeekByTimestamp` 由 `sync.RWMutex` 保护，可被多个执行体并发调用。整批追加在同一临界区内完成校验与写入，要么全部生效，要么完全不留痕。

### 本地验证

```bash
# 全量测试（含竞态检测，重复运行）
go test -race -count=3 ./...

# 查看逐步输入、位点与判定依据日志
go test -race -v ./timelog

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

测试覆盖非单调时间戳、命中/未命中、空日志与相等时间戳等边界、三类非法输入及拒绝后状态不变、随机数据与朴素扫描对拍、查询结果单调性、以及并发追加下首个命中位点稳定。
