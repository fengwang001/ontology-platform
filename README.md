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

## Modbus RTU 帧接收状态机

`modbusrtu` 包实现了带注入时钟的 Modbus RTU 帧接收器：按 t1.5/t3.5
静默间隔切帧、校验 CRC-16/MODBUS，并可精确复现帧边界、帧内间隔违规与
各类丢帧原因（过短 / CRC 错误 / 过长 / 间隔违规 / 他站忽略）。

- 设计细节（阈值公式、结算顺序、CRC 参数、事件与计数语义）见
  `modbusrtu/DOC.md`
- 本地验证：`go test -v ./modbusrtu`（含与逐事件朴素模拟器的随机对照
  及输入/输出/判定依据日志），竞态检测用 `go test -race ./modbusrtu`
