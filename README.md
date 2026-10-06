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

## 分层配置系统（layercfg）

`layercfg/` 提供全局→环境→区域→实例四层的版本化配置：多层覆盖/追加合并、
显式取消、层级锁定、原子批量发布、历史回滚与按版本解析。

- 使用说明：`layercfg/README.md`
- 设计说明（关键取舍、放弃方案、性能与并发论证、本地验证）：`docs/design.md`
- 随机逐步对照测试（独立朴素模型，打印每步输入/输出/判定依据）：
  `go test -v -run TestRandomDifferential ./layercfg/`
