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

## 分层压实输入选择器（`compaction` 包）

`compaction.Picker` 按层得分挑选待压实层，用每层轮转指针选择起点文件，
推出下一层重叠文件集合与可扩张的同层输入集合。相同调用序列重放得到
完全相同的选择与指针；所有方法可并发调用，效果等价于某个串行顺序。

### 选层规则

- 层号为 1 至 L（L ≥ 2），仅第 1 至 L−1 层可作为压实来源，第 i 层容量
  上限为 `cap[i]`（正整数字节）。
- 每层得分 = 本层总字节 / `cap[i]`，用交叉相乘（大整数）精确比较，
  不使用浮点。得分最大者胜出，并列时取层号较小者。
- 最大得分小于 1 时返回 `ErrNothingToCompact`（无事可做）；恰等于 1
  可以压实。

### 起点文件规则

- 本层文件按最小键升序排列，取第一个最小键**严格大于** `ptr[i]` 的文件；
  `ptr[i]` 为「无」时取第一个文件；没有满足条件的文件则回绕取第一个。
- 因此 `ptr[i]` 恰等于某文件最小键时，选中的是下一个文件。

### 重叠集与扩张规则

- 重叠集 O：第 i+1 层中与起点文件闭区间相交的全部文件（端点相等算相交）。
- R 为起点与 O 合起来的最小闭区间；T 为第 i 层中与 R 相交的全部文件
  （含起点）。同时满足以下三条时本层输入取 T，否则只取起点：
  1. T 多于起点一个文件；
  2. T 与 O 的字节总和**严格小于**扩张上限 X（等于 X 不扩张）；
  3. 第 i+1 层中与 T 的并范围相交的文件集合恰等于 O（不扩大）。

### 指针推进

- 选中后 `ptr[i]` 置为本层输入文件中最小键最大者的最小键。
- `Pick` 返回本层输入与 O，均按最小键升序。

### 校验与拒绝

- 构造时依次校验：L ≥ 2、cap 个数恰为 L−1、cap 全为正、X 为正。
- `AddFile` 按以下顺序只报第一个错误：层号越界、最小键大于最大键、
  字节数非正、编号重复（全局唯一）、与同层已有文件相交。
- 每类拒绝都有可区分的哨兵错误（`errors.Is` 可判定）；被拒绝的操作
  不改变文件集合与任何指针。

### 本地验证

```bash
# 全部单测（含得分恰等于 1/略小于 1、并列取小层号、指针等于最小键时
# 取下一个、回绕、扩张三条件各自不满足、指针推进等用例）
go test ./compaction/

# 对拍测试：随机操作序列与逐步朴素实现逐步比对，日志打印输入、
# 输出与判定依据
go test -v -run TestDifferentialAgainstNaive ./compaction/

# 重放确定性 + 并发安全（竞态检测）
go test -race -run 'TestReplayDeterminism|TestConcurrentAccess' ./compaction/
```
