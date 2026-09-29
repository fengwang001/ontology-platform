# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## gapdetector：按序号连续性的跳洞检测器

`gapdetector` 包检测序号流中确认丢失的序号（洞），可容忍不超过乱序窗口的轻微乱序，且重复段不误报。

### 状态与精确规则

检测器维护三个状态：

- **水位 watermark**：已确认连续前缀上界，单调不减；
- **在途集 inflight**：已到达但超前于水位的序号；
- **洞集 holes**：已确认丢失的序号，是水位之内的子集，一经判定绝不回撤。

摄入序号 `seq` 的精确规则（设窗口为 `W`）：

1. `seq <= watermark`：按重复/迟到忽略，不改变任何状态；
2. 否则 `seq` 加入在途集并收敛，循环执行：
   - 候选序号 `watermark+1` 在在途集中：并入连续前缀（`watermark++`）；
   - 否则若在途集最大值 `maxInflight > (watermark+1) + W`：把 `watermark+1` 判为洞并推进水位；
   - 否则收敛完成（此时 `maxInflight <= watermark+1+W`）。
3. 边界含义：在途最大值恰等于 `候选序号+W` 时不判洞，再超过 1 才判洞。

非法输入整体拒绝且原因可区分（`errors.Is` 判定），一次失败不改变水位、在途集与洞集：

- `ErrInvalidSeq`：序号不在 `[MinSeq, MaxSeq]`；
- `ErrInvalidWindow`：窗口超过 `MaxWindow`；
- `ErrOverflow`：`序号+窗口` 超出 `MaxSeq`。

摄入、查询（`Watermark`/`Inflight`/`Holes`）与 `SelfCheck` 均可并发调用。

### 本地验证（全集对照）

`TestFullSetCrossCheck` 演示了用全集对照核对结果的方法：

1. 构造全集 `1..n`，随机扣掉一个子集作为真实丢失；
2. 将剩余序号按窗口有界乱序（按 `W+1` 分组、组内洗牌）后喂入检测器；
3. 核对：`Holes()` 必须与被扣子集完全一致，且 `([1..水位] \ 洞集) ∪ 在途集` 恰好重建实际喂入集合。

运行：

```bash
go test -v -run TestFullSetCrossCheck ./gapdetector
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
