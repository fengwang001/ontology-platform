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

`compaction.Picker` 维护 L 层（层号 1..L，L ≥ 2）文件，第 i 层（1 ≤ i ≤ L−1）
容量上限为 `cap[i]`，扩张上限为 X。文件含编号（全局唯一）、层号、闭区间
`[Min, Max]`（字节串，端点相等也算相交）与字节数；同层文件互不相交。

### Pick 规则

- **选层**：对每层 i 计得分 `本层总字节 / cap[i]`，用交叉相乘（大整数）
  精确比较，取得分最大的层，并列取层号小者；最大得分严格小于 1 时返回
  `ErrNothingToCompact`，恰等于 1 可压实。
- **选起点**：本层按最小键升序，取第一个最小键严格大于 `ptr[i]` 的文件；
  指针为「无」取第一个文件；没有满足条件的文件则回绕取第一个。
- **下层重叠集 O**：第 i+1 层中与起点文件范围相交的全部文件。
- **扩张**：R 为起点与 O 合起来的最小闭区间，T 为本层与 R 相交的全部文件。
  当 T 多于起点、T 与 O 字节总和严格小于 X、且第 i+1 层与 T 并范围相交的
  文件集合恰等于 O 时，本层输入取 T，否则只取起点。
- **指针推进**：选中后 `ptr[i]` 置为本层输入文件中最小键最大者的最小键；
  返回本层输入与 O，均按最小键升序。

### 错误与并发

构造参数非法（`ErrTooFewLevels` / `ErrCapCountMismatch` / `ErrCapNonPositive`
/ `ErrXNonPositive`）与 `AddFile` 校验失败（`ErrLevelOutOfRange` /
`ErrBadRange` / `ErrNonPositiveSize` / `ErrDuplicateID` / `ErrOverlap`，
按此顺序只报第一个）都是可区分错误，可用 `errors.Is` 判定；被拒绝的操作
不改变文件集合与任何指针。所有方法在互斥锁保护下执行，并发调用等价于
某个串行顺序，相同调用序列重放得到完全相同的选择与指针。

### 本地验证

```bash
# 单元测试 + 朴素实现随机对拍（含竞态检测）
go test -race ./compaction/

# 查看对拍日志（每步输入、输出与判定依据）
go test -run TestDifferential -v ./compaction/
```
