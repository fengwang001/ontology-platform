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

## 沿有序二级索引的批量更新执行器（`ontology` 包）

`ontology` 包实现了一个内存表（整数主键 + int64 列 + 一个可声明唯一的有序二级索引）
及其批量更新执行器 `Table.Update`，语句语义为：

- 按索引列的左闭右开区间 `[lo, hi)` 沿索引做区间扫描，可选叠加其他列过滤；
- 赋值支持「索引列乘/加一个常量」或「设置其他列为常量」，均为 int64；
- 返回更新行数与考察的索引条目数，考察条目数等于更新前落在区间内的条目数。

### 恰好一次的保证方式

更新会改变扫描所用的索引键时（Halloween 问题），执行器先沿索引完成区间扫描，
把 `[lo, hi)` 内的条目（按索引顺序）一次性收集为快照，之后只按主键回访这些行
并逐行应用赋值。被移到扫描前方、或移出区间再移入区间的行不会被再次考察，
因此每行恰好更新一次，结果与「先按更新前快照选出全部行再逐行更新」的朴素语义
完全相同（测试中用 `naiveApply` 参考实现逐行对照）。

### 唯一性判定时机

唯一索引的唯一性在整条语句结束时判定：语句中途产生的暂时重复不算冲突
（例如唯一列 1..5 整体加 1 是合法的）；仅当语句结束时仍存在重复键才以
`ErrUniqueViolation` 整体拒绝。

### 回滚语义

以下情况整条语句被拒绝并逐项回滚（按应用逆序恢复每一行的值与索引条目），
表与索引恢复到语句开始前的状态，错误原因可用 `errors.Is` 区分：

- `ErrInvalidRange`：`lo > hi`（`lo == hi` 为空区间，合法且更新零行）；
- `ErrUnknownColumn`：过滤或赋值引用了表结构中不存在的列；
- `ErrOverflow`：任一行的乘/加计算发生 int64 溢出；
- `ErrUniqueViolation`：语句结束时唯一索引上仍有重复键。

### 并发与确定性

语句彼此串行生效：更新持有写锁，读者持有读锁，并发读者只能看到某条语句
之前或之后的完整表；任何时刻每一行在索引中恰好出现一次且键与行值一致
（`Table.CheckInvariants` 可校验）。同一语句序列反复执行结果完全相同。

### 本地验证方法

```bash
# 全部测试（含恰好一次、回滚、并发读者、确定性重放等用例，
# 日志中会打印每个用例的输入、输出与判定依据）
go test -v ./ontology/

# 带竞态检测
go test -race ./ontology/

# 单个用例，例如「乘以 2 跳到扫描前方」
go test -v -run TestMultiplyTwoSkipsAhead ./ontology/
```
