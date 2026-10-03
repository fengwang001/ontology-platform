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

## 周期任务链端到端时延分析器

`chain/` 包实现“释放读取、延后写出”语义下周期任务链的最大/最小反应时间与
最大数据年龄分析，以及只调下游相位的 Tune 搜索（同刻可见、H≤5000、穷举积
≤1000、严格更优才提交、并列取字典序最小）。定义、拒绝原因顺序与本地验证
方法见 [`chain/README.md`](chain/README.md)，含 2000 组对朴素事件传播
模拟器的随机对拍（固定种子、带输入/输出/判定日志）。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
