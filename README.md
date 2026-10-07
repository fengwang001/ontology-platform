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

## 快照损坏修复与动作重放（`recovery` 包）

`recovery/` 实现快照部分损坏下、以动作日志重放修复对象状态的原子性协调器：

- 快照记录逐条 CRC 校验，损坏按**对象**标记为快照时刻不可读（与“对象不存在”区分）。
- 动作记录整帧 CRC 校验，损坏则整条不可采信；完整动作在“涉及对象全部具备已知
  起点或被本动作显式赋予起点锚点”时才整体生效，否则对全部涉及对象整体放弃。
- 报告为对象级，来源三分类：`snapshot` / `rebuilt` / `unknown`，重建不向锚点
  动作之前回溯。
- 拒绝优先级：对象超出覆盖范围 > 快照与日志版本不衔接。
- 并发修复、查询与新动作追加共用互斥，可观察结果等价于某个全局串行顺序。
- 来源定位只读物化投影，O(1)、零扫描、零额外分配，与日志长度无关（基准断言
  见 `TestLookupScaleIndependent`）。

关键文件：

- `recovery/wire.go`：带逐条校验的快照/日志帧编解码。
- `recovery/coordinator.go`：加载、原子性判定、增量投影、请求处理与并发控制。
- `recovery/naive.go`：独立朴素重放参考模型，仅用于差分对拍测试。
- `recovery/DESIGN.md`：设计说明（关键取舍、被放弃方案、验证方法）。

运行：

```bash
go test -race -v ./...
go test -run TestRandomDifferentialVsNaive -v ./recovery
```
