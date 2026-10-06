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

## 特种设备检验周期与超期管控系统（`speq`）

本仓库新增的 `speq` 包实现锅炉、压力容器及安全附件（安全阀、压力表）的
检验有效期、封存暂停计时、整改限期与可使用性判定。

- 设计与取舍：`docs/design.md`
- API 说明：`docs/api.md`
- 朴素对照模型：`speqtest/naive`（独立日历实现、线性扫描）
- 业务演示（打印输入/输出/判定依据）：`go run ./cmd/demo`

### 快速验证

```bash
go test ./...                 # 场景测试 + 40×1500 步随机差分 + 两档性能对照
go test -race ./...           # 并发竞态检测
go run ./cmd/demo             # 完整业务流日志（另存 testlogs/demo.log）
```

随机差分与性能对照的日志写入 `testlogs/`：

- `testlogs/diff_random.log`：随机操作序列的输入、输出与错误码
- `testlogs/perf_two_tiers.log`：对象 2k/20k、命中数相同的查询耗时对照

### 目录

```
speq/                 系统实现（按职责拆分的多个小文件）
speqtest/             随机差分、场景性能验证
speqtest/naive/       独立朴素参考模型
cmd/demo/             业务流演示程序
docs/                 设计说明与 API 文档
testlogs/             测试与演示日志产物
```
