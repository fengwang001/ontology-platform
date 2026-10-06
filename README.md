# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 机组值勤合规包

`package crew` 提供机组人员、机型资质、值勤期登记、延长、撤销以及滚动窗口合规判定：

- 使用 `NewSystem(Config)` 创建不可变配置的合规系统。
- 使用 `AddPerson`、`SetQualification`、`RevokeQualification` 维护人员与资质。
- 使用 `Register`、`Extend`、`Revoke` 变更值勤记录。
- 使用 `NextReport` 查询给定航段数与资质下的最早可报到时刻。
- 使用 `SetLogger(io.Writer)` 打印每次变更的输入、输出与拒绝原因。
- 详细取舍见 [DESIGN.md](DESIGN.md)。

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

若默认 Go 缓存目录只读，可使用：

```bash
GOCACHE=/tmp/go-cache go test ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
