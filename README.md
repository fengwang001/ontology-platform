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

## 闭包捕获变量开闭管理器（`capture` 包）

`capture` 包在值栈上实现闭包捕获变量的“开放共享 / 关闭快照”语义。

- **值栈**：槽号从 0 起；`Push` 返回新槽号且栈顶加一。
- **捕获与共享**：`Capture(slot)` 在同一槽上已有开放变量时返回同一句柄，否则新建句柄；句柄从 1 起递增、永不复用，每次捕获持有数加一。
- **开放状态**：开放变量没有自身存储，经句柄读写与栈槽直接读写互相可见。
- **关闭**：`CloseFrom(level)` 把所有槽号 `>= level` 的开放变量的当前槽值快照进自身存储并转为关闭，随后栈顶置为 `level`；`level == 栈顶` 是合法空操作。关闭后句柄只读写自身存储，与栈（含同槽重新压栈）互不影响。
- **持有数与摘除**：`Release` 使持有数减一；降到 0 时若仍开放则从共享表摘除，之后同槽再捕获得到新句柄。已摘除/已释放完的句柄再读写均无效。
- **可区分的拒绝原因**（被拒绝操作不改变任何状态）：
  捕获槽不小于栈顶、关闭层越界、栈槽越界、句柄不存在、句柄已释放完（后两者互斥）。
- **并发与可复现**：全部方法线性一致，等价于某个串行顺序；相同操作序列重放得到完全相同的句柄与取值。

### 本地验证

```bash
# 点名场景 + 随机差分（逐步与朴素模拟对照，-v 打印每步输入/输出/判定依据）
go test -race -v ./capture

# 只看并发与确定性重放
go test -race -v ./capture -run 'TestConcurrent|TestDeterministicReplay'

# 长时间 fuzz 差分
go test -fuzz=FuzzDifferential -fuzztime=30s ./capture

# 覆盖率
go test -coverprofile=coverage.out ./capture
go tool cover -func=coverage.out
```

若 `go` 不在 `PATH`，可临时使用 `/usr/local/go/bin/go`；
默认构建缓存只读时设置 `GOCACHE=/tmp/gocache`。
