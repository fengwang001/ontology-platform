# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

本仓库当前交付的是其中的**成绩复核与改分审计引擎**（包 `ontology`）。

## 成绩复核与改分审计引擎

每个（学生、课程、学期）有一条成绩记录，记录由**版本链**构成；时间为单调整数时刻。

- `EnterScore`：授课教师初始录入（版本来源 `initial`）。
- `ApplyReview` / `RejectReview`：学生在复核窗口（终点取等）内申请；同一记录至多一份未结案申请。
- `CreateProposal` / `ApproveProposal` / `DenyProposal`：授课教师基于受理申请提案；分数范围与
  单次改分上限均取等校验；审批人不得是本人且级别达标；超过审批时限（终点取等）未审批的提案，
  在下次触达该记录的变更操作时**惰性失效**，申请回到受理中。
- `LockTerm`：学期锁定（不可撤销），进行中的申请结案、待审批提案作废；锁定后仅可
  `SpecialFirst` + `SpecialSecond`：两名互异、均达更高级别的审批人先后确认，第一人确认在
  有效期内（终点取等）才生效；特殊通道不受单次改分上限约束。
- `EffectiveAt` / `TermAverageAt`：只读时点回看，返回当时有效分数、来源与是否复核中；
  不推进时钟、不触发失效。

错误为带固定码的 `*CodedError`，优先级：
`invalid_parameter > clock_rewind > record_not_found > term_locked > forbidden >
window_or_timeout_expired > state_conflict > score_out_of_range`。
被拒操作不改动状态、不写审计；`Audit()` 返回只追加账本。

设计取舍见 [DESIGN.md](DESIGN.md)。

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
