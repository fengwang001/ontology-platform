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

## watchnorm：递归目录监视事件归一化器

`watchnorm` 包把底层监视器的原始事件（创建、删除、移出、移入、溢出）
归一化为四类输出事件：`Created`、`Deleted`、`Renamed`、`Rescan`。
构造参数为监视名额上限 `W`（≥1）与重命名配对窗口 `P`（≥1）：

```go
n, err := watchnorm.New(W, P)
evs, err := n.Created(now, "a/b", true)   // 其余方法：Deleted / MovedFrom / MovedTo / Overflow / Tick
```

### 监视名额预算与补位

- 根目录 `""` 永远存在、永远被监视并占 1 个名额；任意时刻被监视
  目录总数（含挂起移出子树内的）不超过 `W`。
- 新建目录在名额有空时立即被监视，否则记入未监视集合。
- 名额释放（删除子树、挂起记录到期、Overflow 丢弃挂起）后统一补位：
  只要名额有空且存在未监视目录，就把路径字节序最小者提升为被监视，
  并产生一个 `Rescan(path)` 事件，重复至名额满或无可补。

### 重命名配对

- `MovedFrom` 把条目连同整棵子树摘下成为挂起记录（不产生事件，
  其中被监视目录仍占名额）；`MovedTo` 找到相同 cookie 且 `now-t < P`
  的记录即配对：子树按路径段边界整体改前缀挂回，监视状态保持不变，
  产生 `Renamed(from, to)` 并补位。
- 无记录（或记录已到期）时 `MovedTo` 按新创建处理：产生 `Created(path)`，
  目录额外再产生 `Rescan(path)`（子目录内容未知）。
- `Overflow` 先产生 `Rescan("")`，再丢弃全部挂起记录（不产生
  `Deleted`，名额释放），最后补位。

### 入口处理次序

每个操作（含 `Tick`）都按同一次序开始：

1. `ErrClock`：`now` 小于此前任一次的 `now` 时拒绝且不改任何状态；
2. `ErrBadArg`：路径 / cookie 参数检查；
3. 入口处理：所有 `now-t >= P` 的挂起记录按 `(t, cookie)` 升序到期，
   各产生一个 `Deleted(path)` 并释放名额，全部到期后统一补位。

入口处理的状态变化在操作随后因状态类错误（`ErrNoParent`、`ErrExists`、
`ErrUnknown`、`ErrDupCookie`、`ErrMismatch`）被拒绝时仍然保留，其事件
与错误一并返回。错误优先级：`ErrClock` > `ErrBadArg` > 入口处理 >
各操作的状态类错误。

### 本地验证

```bash
# 全部确定性用例（规范示例、边界、错误优先级等）
go test ./watchnorm/

# 2000 组随机序列与朴素实现逐步对照（-v 打印输入、输出与判定依据）
go test -run TestRandomAgainstNaive -v ./watchnorm/

# 并发等价性与名额不变量（竞态检测）
go test -race -run TestConcurrent ./watchnorm/
```

对照测试将同一随机操作序列同时重放到增量实现（`Normalizer`）与独立
编写的朴素实现（`Naive`，嵌套节点树 + 全量遍历计数），逐步要求事件
序列、返回错误与内部状态完全一致，且被监视目录数始终 ≤ `W`。
