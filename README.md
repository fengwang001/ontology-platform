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

## 需求响应邀约与履约考核（dr 包）

`dr/` 实现需求响应邀约与履约考核系统：运营方发布削减事件并邀约参与者，
参与者承诺削减量并在事件窗口内履约，系统按历史用电推定基线（资格日均值 +
同日校正）、考核履约率并结算报酬与违约金。设计取舍见 `DESIGN.md`。

```bash
go test ./dr/          # 边界测试 + 朴素模型随机对照
go test -race ./dr/    # 并发安全
go test ./dr/ -run TestRandomDifferential -v  # 每条操作的输入/输出/判定日志
```
