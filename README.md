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

## loader：浏览器资源加载调度器

`loader/` 实现资源请求登记、每源连接配额、优先级抢占、预加载缓存与完成通知五个协作模块，
设计说明见 `docs/loader-design.md`。

```bash
# 场景测试 + 朴素模型随机对照（日志含输入/输出/判定依据）
go test ./loader -race -v

# 性能证明：选择与缓存命中开销不随规模增长
go test ./loader -run xxx -bench .
```
