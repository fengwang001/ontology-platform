# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前交付：**特种设备检验周期与超期管控系统**——管理锅炉、压力容器及其
安全附件（安全阀、压力表）的检验有效期、封存暂停计时、整改限期与可使用性判定。
设计说明见 `docs/design.md`。

## 模块

- `calendar` — 日序号 ↔ 公历换算、日历月加法
- `domain` — 类别/配置/状态/检验结论/错误类型
- `index` — 按类别分桶的到期日有序索引（区间查询 O(log n + k)）
- `system` — 核心服务：登记、检验、封存/启封、挂接/转移/摘除、使用登记、预警、报废
- `naive` — 朴素对照模型（差分测试用）
- `cmd/server` — 演示程序

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 直接运行演示
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

# 随机操作序列差分对照（正式实现 vs 朴素模型，日志含输入/输出/判定依据）
go test -v ./naive/ -run TestDifferential

# 复杂度两档对照（1 万 vs 10 万对象）
go test -v ./index/ -run TestRangeComplexity
go test -v ./system/ -run 'TestWarnScales|TestUsableScales'

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
