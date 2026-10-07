# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `retention/`：对象逻辑删除与可见性服务（四态状态机、宽限期撤销、
  保留期冻结、按身份的可见性、出边跟随源对象、审计日志）。
  设计与取舍见 `retention/DESIGN.md`。

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
