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

## cors 包：跨源预检判定与预检结果缓存内核

`cors/` 实现跨源请求的五部分协作内核：请求分类、预检必要性判定、
预检结果缓存、响应校验与凭据模式处理。设计取舍见 `cors/DESIGN.md`。

```bash
go test -race -v ./cors/                 # 单测 + 朴素模型随机差分对照（日志含输入/输出/判定依据）
go test -run xxx -bench . ./cors/        # 性能证明：分类与缓存命中均为 O(1)
```
