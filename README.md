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

## 计数型滑动窗口前 K 高分（`topk` 包）

`topk` 包提供计数型滑动窗口前 K 高分维护结构 `Window`，源码见 `topk/topk.go`。

### 窗口口径

- 窗口保留最近 N 条变更（`Change{Key, Delta}`），超出容量时最旧一条立即滑出并撤回其分值贡献。
- 键的分值等于其在窗口内所有变更的分之和（增量求和）。
- 键在窗口内至少有一条变更才算存在；分值为 0 的变更同样使键存在，最后一条变更滑出后键即被移除。

### 排序与并列规则

- 前 K 按分值降序排列；分值并列时按键名字典序升序。
- 窗口滑出最旧变更时及时撤回掉分键，并补位新冒出的键，结果可复现。

### 非法输入

- 窗口大小或前 K 数为非正、前 K 超过窗口、键为空，分别返回可区分的哨兵错误
  `ErrNonPositiveWindow`、`ErrNonPositiveK`、`ErrKExceedsWindow`、`ErrEmptyKey`（可用 `errors.Is` 判定）。
- `Apply` 先校验整批再落库：任一条被拒时整批变更都不生效，窗口与分值不变。

### 并发与自检

- `TopK` 与 `SelfCheck` 均为只读加锁（`sync.RWMutex`），可并发调用；
  同一实例在并发只读下得到的结果逐字段完全一致。
- `SelfCheck` 依据窗口队列重算全部分值与排序视图，与增量维护状态逐项比对。

### 本地验证

```bash
# 运行 topk 包全部测试（含竞态检测与详细日志，日志含输入、结果与判定依据）
go test -race -v ./topk

# 指定场景
go test -race -v -run TestEvictionRevokesAndBackfills ./topk
```
