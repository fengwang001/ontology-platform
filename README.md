# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `ontology/`：嵌套动作调用引擎。动作可触发其它动作，支持前置条件独立评估
  （内层可见外层未提交的写入计划）、关键/非关键调用的失败传播与整体放弃、
  自我触发检测、输出依赖的注册期静态校验、并发链条的可序列化提交，
  并为每次调用记录完整审计轨迹。设计取舍与验证方法见
  [docs/design.md](docs/design.md)。
- `cmd/server/`：最小 HTTP 演示服务（`POST /v1/actions/{name}/execute`、
  `GET /v1/objects/{id}`、`GET /v1/commits`）。

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
