# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `enrollment`：选课容量与课程关系约束引擎（容量、先修、共修、互斥、
  学分上限、时间冲突；批量全有或全无、级联退课、原子换课）。
  设计说明见 [enrollment/DESIGN.md](enrollment/DESIGN.md)。

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
