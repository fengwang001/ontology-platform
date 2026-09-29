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

## 计数型滑动窗口前 K 高分（`tophits` 包）

`tophits.SlidingTopK` 对最近 **N 条变更**维护每个键的分值之和，并持续输出分值最高的前 **K** 个键。

- **窗口口径**：窗口只保留最近 N 条 `Change{Key, Score}`（按到达先后）。新变更进入时若窗口已满，最旧的一条立即滑出；`Apply` 接收一个批次，批次内按顺序逐条进入，批次中间也会发生滑出。窗口内不足 N 条时按实际条数计算。
- **分值求和**：键的分值 = 其在当前窗口内全部变更分值的代数和（`int64`，允许负分与零分）。键只要在窗口内还剩至少一条变更就算“存活”；最后一条变更滑出后该键立即删除，即使此前累计为 0 也不保留。滑出一条变更时会从该键分值中减去其分值（撤回），掉出榜单的键被移除、新冒出的键自动补位。
- **排序与并列规则**：前 K 按分值**降序**；分值相同时按键名**字典序升序**（Go 字符串字节序比较），因此结果完全确定、可复现。存活键不足 K 个时返回全部存活键；空窗口返回非 nil 的空切片。
- **非法输入整体拒绝**：以下情况返回可区分的哨兵错误（用 `errors.Is` 判断），且窗口与分值完全不变——
  - `ErrNonPositiveWindow`：N 非正；`ErrNonPositiveK`：K 非正；`ErrKExceedsWindow`：K > N（构造期拒绝）。
  - `ErrEmptyKey`：批次中任一条变更键为空（错误信息附带其下标）。校验先于任何状态修改，整批不生效；空批次（nil/长度 0）是成功的无操作。
- **并发语义**：内部使用 `sync.RWMutex`，`TopK` / `Snapshot` / `SelfCheck` 为只读调用，可与写入并发执行；多次只读调用在窗口无写入期间得到逐字段完全一致的结果（返回值均为拷贝，不含内部引用）。需要“全量与前 K 严格来自同一状态”时使用 `SnapshotWithTopK()`（同一把读锁内产出）。
- **自检**：`SelfCheck()` 遍历环形缓冲重算每个键的分值与出现次数，与维护中的映射逐项核对（环形槽位、非正计次、幽灵键、汇总不一致都会报错），可在生产环境并发调用。

### 最小用法

```go
sw, err := tophits.NewSlidingTopK(100, 10) // N=100, K=10
if err != nil { /* N 非正 / K 非正 / K>N */ }

if err := sw.Apply([]tophits.Change{
    {Key: "user:1", Score: 3},
    {Key: "user:2", Score: -1},
}); err != nil {
    // ErrEmptyKey：整批未生效
}

top := sw.TopK()   // []tophits.Entry{{Key, Score}...}
err = sw.SelfCheck()
```

### 本地验证

```bash
# 若 go 不在 PATH（本机安装在 /usr/local/go）
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache   # 仅当默认缓存目录只读时需要

go test -race -v ./tophits/                       # 详细用例，日志含输入/结果/判定依据
go test -race -count=5 ./...                      # 重复运行验证并发稳定性
go test -cover ./...                              # 覆盖率
go vet ./... && gofmt -l .                        # 静态检查与格式
```

测试覆盖：滑出撤回、部分撤回重排、归零消失与补位、并列字典序与 K 截断、负分/零分存活、非法参数与空键整批拒绝、存活键少于 K、批次跨滑出边界，以及只读逐字段一致性与读写竞争（`-race`）。
