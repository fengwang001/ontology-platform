# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

本仓库当前交付 `hd/` 包：**血液透析中心机位排程与感染隔离系统**。
设计细节、取舍与被放弃方案见 [`DESIGN.md`](./DESIGN.md)。

## 血液透析排程系统（`hd`）

时间以整数分钟计（`0` 到 `10^7`），机位分普通区/隔离区，患者感染状态为
阴性、乙肝阳性、丙肝阳性、待定。系统支持单调时钟、感染隔离与观察位约束、
常规/深度消毒占用、患者最短恢复间隔、周期方案展开（同机位优先、全有或全无）、
机位故障改派、感染状态变更重核、取消与消毒释放，所有操作并发安全且可确定性重放。

### 最小用法

```go
sys, err := hd.New(hd.Config{
    CleanNegative: 30, CleanHBV: 40, CleanHCV: 50,
    DeepClean: 120, MinRecovery: 60,
})
_ = sys.RegisterBay(0, "I1", hd.ZoneIsolation, false, true)
_ = sys.RegisterPatient(1, "p1", hd.InfectionHBV)

bay, err := sys.BookTreatment(2, "t-1", "p1", 1000, 240)
// bay == "I1"；下一次治疗在该机位上最早开始于 1000+240+40
```

错误统一为 `*hd.Error`，可断言 `Code`（优先级从低到高）：

```go
var e *hd.Error
if errors.As(err, &e); e.Code == hd.ErrNoFeasibleBay { ... }
// ErrInvalidArgument < ErrClockRewind < ErrNotFound < ErrInvalidState
// < ErrIsolationConflict < ErrPatientConflict < ErrNoFeasibleBay
```

### 操作清单

| 方法 | 说明 |
| --- | --- |
| `New(cfg)` | 创建系统；各消毒/恢复时长必须为正整数 |
| `RegisterBay(now,id,zone,observed,available)` | 登记机位 |
| `RegisterPatient(now,id,infection)` | 登记患者 |
| `BookTreatment(now,tid,pid,start,dur)` | 单次治疗，返回机位 |
| `ApplyPlan(now,id,pid,weekdays,dayStart,dur,from,to)` | 周期方案，返回每次机位 |
| `CancelTreatment(now,tid)` / `CancelPlan(now,id)` | 取消（同步释放消毒占用） |
| `ReportFault(now,bay,at)` / `RecoverBay(now,bay,at)` | 故障停用/恢复（原子改派） |
| `ChangeInfection(now,pid,newInf,at)` | 感染状态变更与未开始治疗重核 |

### 验证

```bash
# 单元测试（覆盖消毒边界、深度消毒、观察位、方案全有或全无、
# 故障改派与整体拒绝、状态重核、取消释放、错误优先级）
go test ./hd/ -v

# 与独立朴素模型差分对照 1500 组随机操作序列；逐步日志写入 hd/diff_runs.log
go test ./hd/ -run TestDifferentialNaive -count=1
DIFF_LOG=/tmp/diff.log go test ./hd/ -run TestDifferentialNaive

# 竞态检测（含并发串行化用例）
go test -race ./hd/

# 复杂度双规模对照（treap 生产实现 vs 朴素线性扫描）
go test ./hd/ -run xxx -bench . -benchtime=2000x
```

实测对照（单次机位寻找，历史从 1 千扩大到 10 万）：

| 实现 | 1 千历史 | 10 万历史 |
| --- | --- | --- |
| 确定性 treap（生产） | ~264 ns/op | ~264 ns/op |
| 朴素全量扫描 | ~122 µs/op | 随历史线性增长 |

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
