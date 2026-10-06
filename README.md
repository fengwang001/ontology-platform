# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 营业时段与临时歇业系统

根包 `shophours` 实现商家周营业时段版本化、临时歇业、强制停业、即时单/预约单准入与
订单取消编排。设计取舍见 `DESIGN.md`，API 说明见 `DOC.md`；测试含独立朴素逐秒模型的
随机差分对照（`go test -run TestRandomDifferential -v` 打印每步输入、输出与判定依据）。

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
