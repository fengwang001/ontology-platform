# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 动作事务审计溯源子系统

位于 `audit/`，由动作执行（`executor.go`/`store.go`）、只增审计存证
（`log.go`）与重放重建（`replay.go`/`naive.go`）三部分组成：

- 每对象类型内序号严格递增、无空洞；提交/回退/订正都占序号，回退记录
  `Before==After`，重放时跳过；审计写入失败不占序号且状态一致回退。
- 审计记录先落盘（SHA-256 哈希链只增存证），状态随后在同一临界区提交，
  杜绝“状态已变审计缺失 / 审计存在状态未变”。
- 订正以新记录追加并覆盖原记录对后续重建的影响，原记录原样保留；订正
  不得指向另一条订正。
- 周期快照使区间 `(from,to]` 重放只与区间长度相关；另维护朴素线性模型
  做随机差分对照。

端到端演示：

```bash
go run ./cmd/auditdemo
```

设计取舍、被放弃方案与验证方法见 `docs/design.md`。

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
