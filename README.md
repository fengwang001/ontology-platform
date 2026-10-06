# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `room/`：多人对局房间生命周期服务（等待→倒计时→进行中→结算中→
  已结束/已作废）。惰性计时、房主迁移、中途退出、一致/争议/超时裁决；
  热点操作 O(1)，附带独立朴素模型与 1600 组随机差分测试。设计与取舍见
  `room/DESIGN.md`。

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
