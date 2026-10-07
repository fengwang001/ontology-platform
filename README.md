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

## 影像预约准入模块

实现位于 `imaging/`，独立朴素模型与随机对照位于 `simulation/`。

### 核心能力

- CT/MR 设备、MR 场强、每日日内质控时段、检查类型、患者和肾功能结果登记。
- 增强检查的肾功能时效、下限、水化阈值和高风险/普通患者 TTL。
- 设备占用 = 检查时长 + 按设备类别配置的清洁时长。
- 增强检查后的全院留观位容量约束。
- MR 植入物最大场强兼容。
- 水化、对比剂过敏预处理登记与签到窗口复核。
- 失败签到转 `needs_reschedule` 并立即释放设备/留观占用。
- 原子改约：旧占用不阻挡新占用，失败恢复原约。
- 单调接受时钟；被拒绝操作不改变状态或时钟。

### 本地验证

本环境 Go 位于 `/usr/local/go/bin/go`，默认构建缓存目录只读，可使用：

```bash
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test ./...
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test -race ./...
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test -v ./simulation
```

边界测试覆盖区间相接/差一分钟、质控跨日重复、TTL 恰等、肾功能上下限恰等、水化和预处理提前量恰等、场强恰等、留观满与恰相接、失败签到释放、改约自身重叠和错误优先级。

随机对照使用 1500 组确定性操作序列，`-v` 时逐步打印输入、真实/朴素输出和判定依据。

两档规模对照：

```bash
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test -run '^$' \
  -bench 'Booking(1000|16000)History' -benchmem -benchtime=1000x -count=1 ./imaging
```

详细设计、复杂度证明、关键取舍和被放弃方案见 [DESIGN.md](DESIGN.md)。
