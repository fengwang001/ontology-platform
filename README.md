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

## auditlog 包

`auditlog/` 实现前向安全的密钥演进审计日志与一趟检查点校验器：写入者
逐条追加由演进密钥保护的数据/换钥/封存记录并封存，持有若干
`(序号, 密钥)` 检查点的校验者单趟校验最小检查点之后的整段日志，精确归类
缺口、回放、封存后追加、时间回退、篡改、内容不符、检查点冲突与尾部截断。
设计与错误判定次序见 `auditlog/DESIGN.md`；随机对照测试：

```bash
go test ./auditlog -run TestDifferentialAgainstNaive2000 -v
```
