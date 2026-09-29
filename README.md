# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 区间批量更新执行器（`ontology` 包）

表有一个 64 位整数主键与一个 64 位整数索引列（索引可声明唯一）。
`Executor.Execute` 执行形如「沿索引列扫描左闭右开区间 `[lo, hi)`，可选按其他列
等值过滤，对命中行做赋值」的语句；赋值支持 `:= 常量`、`+= 常量`、`*= 常量`。

### 恰好一次（exactly-once）的保证方式

- 访问路径是且仅是该有序二级索引上的一次区间扫描，返回 `EntriesExamined`
  恰好等于**更新前**落在 `[lo, hi)` 内的索引条目数。
- 执行时先在索引上一次性取得更新前快照的候选主键列表（按
  `(索引键, 主键)` 有序），然后只对该列表中的行做过滤与计算；之后才开始写。
- 因为命中集合在任何写入之前就已物化，某行的新键即使：
  - 乘 2 后跳到扫描游标**前方**，或
  - 被减法移到 `lo` 以下、之后本可能再回到区间内，

  也不会被第二次考察或第二次更新。语义与朴素实现「先按更新前快照选出全部行，
  再逐行更新」逐行一致。
- 提交时每行做一次索引删除 + 一次索引插入，因此任何时刻每行在索引中恰好出现
  一次，且索引键与行值一致。

### 唯一性判定时机

- 唯一冲突按**整条语句结束时**的整表最终映像判定：先构造全部行更新后的最终键，
  再检查是否有两行同键。
- 语句中途出现的暂时重复（如唯一列 `1..5` 整体 `+1` 或整体 `-1` 时扫描序上的
  瞬时碰撞）不算冲突，两条语句均正常提交。
- 语句结束时若仍有两行落在同一键，返回 `ErrUniqueViolation`。

### 回滚语义

所有新值的计算与唯一性校验全部通过之前不写任何数据，因此下列错误都是整体拒绝，
表与索引逐项恢复原状（`RowsUpdated == 0`），且错误原因可区分：

- `ErrUnknownColumn`：赋值或过滤引用了未知列（含试图改主键）；最先判定。
- `ErrInvalidRange`：`lo > hi`（空区间之前判定）。
- `ErrArithmeticOverflow`：任一命中行的加/乘在 int64 下溢出；典型场景是第 7 行
  溢出时前 6 行也必须回滚。
- `ErrUniqueViolation`：语句结束时仍有唯一冲突。

`lo == hi` 为空区间，更新 0 行、考察 0 个条目。

### 隔离与确定性

- 语句在表级互斥锁下串行生效；读者通过 `Snapshot()` 在读锁下取得深拷贝，
  只能看到某条语句之前或之后的完整表，不会看到中间态（见
  `TestConcurrentReadersSeeBeforeOrAfter`，`go test -race` 验证）。
- 同一语句序列在相同初始数据上反复执行，结果完全相同
  （`TestRepeatedExecutionIsDeterministic`）。
- 每条语句向配置的日志输出三行：输入（区间、过滤、赋值）、输出
  （更新行数、考察条目数或错误）与判定依据（快照物化、语句末冲突、回滚原因等）。

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
go test -run TestMultiplyByTwoMovesAhead ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
