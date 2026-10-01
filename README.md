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

## 日程系列管理器（`schedule` 包）

`schedule` 包实现按月第 N 个星期几重复的日程系列：展开实例、取消/改期单个实例、
以及「此次及以后」拆分系列。完整规则见 `schedule/doc.go` 的包注释。

核心入口（`schedule.Manager`，并发安全）：

- `Create(CreateInput)`：创建系列；规则 `Rule{IntervalMonths:k, Nth, Weekday:w}`，
  终止条件 `Termination{Count}` 与 `Termination{Until}` 二选一。
- `Cancel(id, date)` / `Reschedule(id, date, target)`：按实例原日期取消或改期。
- `Split(SplitInput)`：在实例原日期处拆为「date 之前」（旧系列）与「date 及以后」（新 id/新规则）。
- `Expand(from, to)`：返回 `[from,to)` 内的 `Instance{SeriesID, Original, Actual}`，
  按实际日期、系列 id、原日期升序；区间跨度上限 3660 天。
- 所有拒绝原因以 `*schedule.Error` 的 `Code`（`ErrCode`）区分，例如
  `INVALID_DATE`、`NOT_AN_INSTANCE`、`INSTANCE_ALREADY_CANCELLED`、
  `TARGET_DATE_OCCUPIED`、`EMPTY_RANGE`、`RANGE_TOO_WIDE` 等；被拒绝的操作不改变任何系列。

关键规则：

- start 所在月为第 0 个月，每隔 k 个月取该月第 nth 个星期 w；`nth=-1` 为当月最后一个。
- 早于 start 的候选不算（不顺延）；某月没有第 5 个星期 w 时跳过且不占 count；
  候选晚于 2200-12-31 不再生成；count/until 一律按规则生成的原日期计。
- 取消/改期的实例仍占 count；取消已改期实例会释放其改期日；
  改期目标被同系列另一实例（原位或改期而来）占用时拒绝；
  对已取消实例改期按「取消已取消」同因拒绝。
- 拆分后旧系列：count 型 count 变为 date 之前实例数（可为 0），
  until 型 until 变为 date 前一天（可早于 start）；date 及之后的例外丢弃。
  新系列从 date 起算（首个候选可不等于 date），剩余名额为 `原 count − 之前实例数`，
  until 型沿用原 until。
- 写操作互斥原子执行；展开在读锁内深拷贝快照；相同操作序列重放结果完全一致。

本地验证（如 `go` 不在 PATH，先 `export PATH=$PATH:/usr/local/go/bin`）：

```bash
go test ./schedule/                        # 单元用例 + 200 组随机差分（对照朴素逐日扫描）
go test -race -count=1 -v ./schedule/     # 竞态检测与逐条日志
go test -run 'TestFifthWeekdayMissingSkipped|TestLastWeekday|TestSplit' -v ./schedule/
```

给 `NewManager(logger)` 传入实现了 `Printf` 的日志器（如 `log.New(...)`）即可打印
每次操作的输入、输出与判定依据（候选列表、取消/改期归属、展开跳过原因等）。
