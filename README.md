# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 缓慢变化维历史区间维护（`scd` 包）

`scd` 包对可能**乱序到达**的维度变更事件维护每个键的取值历史区间，
保证历史与批量重算一致、区间良构、与到达顺序无关（可复现），且全部历史
均为**增量维护**而非整体重算。

### 变更点模型

- 每个键维护一组**变更点**（`ChangePoint`）：生效时间 `At`、操作类型
  （`OpUpdate` / `OpDelete`）、取值 `Value`、到达序号 `Seq`。
- 同一键同一生效时间的变更点只保留**后到者**（`Seq` 更大者）：后到的更新
  替换取值，后到的删除把更新点变成删除点，反之亦然。
- 历史完全由变更点集合决定，与事件到达顺序无关。

### 区间生成规则

- 变更点按生效时间升序排列；每个**更新点**产生一个左闭右开区间
  `[At, 下一变更点.At)`，最后一个更新点的区间延伸到 `MaxTime`。
- **删除点不产生区间**：它只闭合前一个更新区间，删除点到下一个更新点之间
  无取值（点查询返回未命中）。
- 相邻两行即使取值相同也**不合并**，每个更新点各自产生一行。
- 乱序事件落在某行内部时**拆分该行**；恰等于已有变更点时**替换该点**。

### 增量维护

每次提交只修补受影响键从变更位置起的局部区间：新点把所在行的右端点收敛
到新点（若新点落在该行内部），再从该位置起由变更点集合确定性重生成后续
行；之前的行不受影响。`SelfCheck` 会校验增量结果与纯重算结果逐行一致。

### 边界与错误类别

提交（`Commit`）是**整批原子**的：任一输入非法则整批拒绝，拒绝后变更点与
历史保持提交前状态，失败不留痕。错误以 `*CommitError` 返回，内含互不相同的
错误码（`ErrorCode`）：

| 错误码 | 含义 |
| --- | --- |
| `empty_batch` | 事件批次为空 |
| `invalid_argument` | 接收者等参数非法（如 nil History） |
| `empty_key` | 事件键为空 |
| `invalid_op` | 操作类型不是 `OpUpdate`/`OpDelete` |
| `time_out_of_range` | 生效时间越出 `[MinTime, MaxTime]`（±2^62 域） |
| `too_many_change_points` | 提交后某键变更点数将超过上限（默认 100000，可用 `WithMaxPointsPerKey` 调整） |
| `self_check_failed` | `SelfCheck` 发现区间不良构或与重算不一致 |

### 并发

`History` 的全部方法（`Commit`、`Intervals`、`ValueAt`、`Snapshot`、
`SelfCheck` 等）均可被多个执行体并发调用，读取与提交可并发进行：
按键加读写锁，多键提交按字典序加锁避免死锁，读取返回拷贝快照。

### 本地验证

```bash
# 全量测试（含竞态检测与逐步日志：每步输入、历史区间与判定依据）
go test -race -v ./scd/

# 代码检查
gofmt -l .
go vet ./...
```

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
