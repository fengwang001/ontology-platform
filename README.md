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

## 通知投递调度器

通知调度器位于 `scheduler` 包，入口为 `scheduler.New`、`Submit` 与 `Poll`。

- 本地分钟使用非负余数：`lm(t)=((t+off) mod 1440 + 1440) mod 1440`；本地日序号对 `(t+off)/1440` 向负无穷取整，负偏移和较小 `t` 下可以得到负日序号。
- `qs < qe` 表示普通静默区间 `[qs,qe)`；`qs > qe` 表示跨午夜区间，条件为 `lm >= qs` 或 `lm < qe`；`qs == qe` 表示无静默。起点包含、终点不包含。
- `shift(t)` 先判断 `t` 是否位于静默；若是，再推进到最近的本地 `qe`。普通批次严格使用 `shift(now+G)`，即先加合并窗口、后做静默顺延。
- 每次操作先校验参数、时钟回退和操作前队列上限，再反复处理最小堆顶 `(f,seq)`。达到当日非紧急上限的批次改为 `shift((d+1)*1440-off)`；若次日零点仍在静默，会在同一次到期循环中继续顺延。
- 紧急通知立即返回，立即占用当日非紧急投递计数但不受 `Cap` 拦截；重要通知总是单独成批；普通通知只并入到期处理后仍存在的同 key 普通批次，新建批次即使 `f <= now` 也等待下一次操作处理。
- 包内测试通过 `batchExamined` 与 `batchDeferred` 记录单次操作的堆顶考察数和顺延数，并分别验证待投总数 1000 与 100000 时，考察数只随到期批次和顺延次数增长。

本地验证：

```bash
GOCACHE=/tmp/go-cache-ontology go test -race -v ./...
GOCACHE=/tmp/go-cache-ontology go vet ./...
```

`TestRandomNaiveSimulation2000` 会重放 2000 组随机操作序列，并用同一规则维护朴素模型；`-v` 日志包含每次输入、输出、错误以及堆序、并入和每日上限顺延的判定依据。
