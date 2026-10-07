# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统：事件溯源孤儿追溯判定（`orphan` 包）

以事件溯源方式重建对象-链接网络在任意历史时刻的状态，并对对象做
追溯孤儿判定：依据调用方声明的级联清理规则版本（而非事件流表面记录）
判断对象在该时刻是否"实质上已成孤儿"，并区分呈现"追溯孤儿但后续仍有
活动"等情形。设计取舍、被放弃方案与验证方法见 [docs/design.md](docs/design.md)。

- `orphan/`：并发安全的事件存储、规则版本账本、索引化判定引擎与网络重建
- `orphan/naive/`：独立朴素重放模型，用于差分对照测试
- `cmd/server/`：端到端演示

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
