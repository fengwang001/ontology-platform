# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 提交签名验证链服务

包 `ontology` 提供基于可信锚点、密钥有效区间与背书关系的提交链裁决。

- 对象与原因：`Commit`、`Tag`、`Signature`、`Endorsement` 定义于 `model.go`；
  九种不可信/未签名原因见 `Reason*` 常量。
- 注入校验器：实现 `SignatureValidator`（`SigValid` / `SigInvalid` / `SigUnknownKey`），
  用 `NewService(validator)` 构造服务；本题不做密码学运算。
- 登记与策略：`AddCommit`、`AddTag`、`RegisterKey`、`AddEndorsement`、`RevokeKey`、
  `SetAnchor`、`SetPolicy`（`RevocationRetroactive` 吊销追溯、`MergeExempt` 合并豁免）。
- 裁决：`VerifyCommit(commitID, verifyAt)` 返回单提交 `CommitVerdict`；
  `VerifyBranch(tipID, verifyAt)` 沿锚点到顶端的第一父链给出全链可信或首个不可信点，
  `BranchVerdict.CommitsTouched` 等于第一父链长度。
- 判定优先级：来自未来 > 时刻倒置 > 伪造 > 密钥未知 > 追溯吊销 >
  未生效 > 已失效 > 已吊销 > 背书链断裂；未签名单列。
- 调用错误（与裁决结果区分）：`ErrInvalidArgument`、`ErrCommitNotFound`、
  `ErrTagNotFound`、`ErrKeyNotFound`、`ErrEndorsementCycle`、`ErrRevocationEarlier`。
- 设计取舍与放弃方案见 `DESIGN.md`。

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
