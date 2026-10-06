# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## PIVAS 排程与稳定期判定

`pivas/` 子包实现医院静脉用药配置中心的排程内核：配伍禁忌拦截、配置后有效期判定、
洁净台批次编组、按时送达裁决、普通/紧急医嘱加入与取消，并保证可串行化复现。
设计取舍与本地验证步骤见 `pivas/DESIGN.md`。

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
