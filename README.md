# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统：事件溯源网络重建与追溯孤儿判定

`ontology/` 包在只追加事件流上重建对象/链接网络的任意历史状态，并按
显式声明的（不可变）级联清理规则版本，追溯判定对象是否“实质上已成
孤儿”——规则上本应被级联清理、但流中并无 `OrphanMarked` 记录。

- 设计说明（关键取舍、被放弃方案、本地验证）：[`docs/DESIGN.md`](docs/DESIGN.md)
- API 使用指南：[`docs/USAGE.md`](docs/USAGE.md)

要点：规则版本不可变、判定结果只依赖 `(流, 对象, T, 钉死版本)` 而幂等；
追溯孤儿与真实级联孤儿分开呈现并列出其后的活动；四类错误固定优先级、
错误零副作用；COW 快照 + 最终化复核保证并发可串行化；判定只扫描对象自身
索引切片，成本不随全网链接事件量增长，并由独立朴素逐事件重放逐条对照、
审计留痕。

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
