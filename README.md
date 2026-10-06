# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 血液透析机位排程与感染隔离系统

本仓库 `hemo/` 实现了一个可精确复现的血液透析中心排程引擎：

- 周期性方案展开与全有或全无的机位分配（同机位优先，否则按开始时刻贪心）；
- 阴性/乙肝/丙肝/待定四态隔离规则，观察位限制，乙肝-丙肝相邻深度消毒；
- 机位故障停用与改派（进行中治疗不动，任一改派失败整次拒绝）；
- 待定确诊与新感染后的未来治疗重新核对；
- 取消单次/整方案并同步释放消毒占用；
- 七类错误按固定优先级只报第一个，单调时钟与回退检测；
- 全部操作可并发，串行化后等价、重放确定；
- 固定时间域 64 叉基数 trie：可行机位判定的时间线探测开销与历史总量无关，
   并有 1,000 / 100,000 两档历史的探测计数断言与基准对照；
- 独立朴素参考模型 + 1,500 组随机操作序列差分测试（逐步 JSONL 日志）。

文档：

- 设计说明（取舍、被放弃方案、复杂度证明、验证方法）：`docs/DESIGN.md`
- 对外 API：`docs/API.md`

快速开始：

```bash
go test -race ./hemo/
go test ./hemo/fuzz/ -run TestDifferential1500 -seqs 1500 -ops 40 -v
go test ./hemo/ -run NONE -bench 'BenchmarkFeasibility|BenchmarkNaiveScan' -benchtime=200x
go run ./cmd/hemodemo
```

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
