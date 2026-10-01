# 固定视图 PBFT 请求日志

`pbft.Log` 实现一个副本在固定视图 `v` 内的请求序号日志。副本数为 `N=3f+1`，主节点为 `v mod N`，初始 `executed=0`，接收窗口为 `executed < s <= executed+L`。

## 两级证书

- `Prepared(s,d)`：序号 `s` 已接受主节点发出的 `PrePrepare(v,s,d,primary)`，并且至少 `2f` 个互不相同的非主节点接受了 `Prepare(v,s,d,from)`。
- `CommittedLocal(s,d)`：`Prepared(s,d)` 成立，并且至少 `2f+1` 个互不相同的副本（可包含主节点）接受了 `Commit(v,s,d,from)`。
- 相同发送者对相同 `(v,s)` 重复发送相同摘要属于空操作，只计一次；不同摘要整体拒绝，不改变任何已有表态。
- `Commit` 可以先于 `PrePrepare` 和 `Prepare` 记录；只有两级条件同时满足时才形成 `CommittedLocal`。

## 执行与窗口

`Execute()` 从 `executed+1` 开始检查。只有当前序号存在满足 `CommittedLocal` 的唯一证书摘要时才执行；执行后 `executed` 加一，并继续检查下一序号。遇到第一个不满足条件的序号立即停止，因此即使更高序号已经提交，也不会越过空洞执行。

每次成功执行后窗口整体后移：新窗口仍为 `executed < s <= executed+L`。已执行序号的迟到消息与超过 `executed+L` 的消息一样被拒绝。返回值是新分配的 `[]Execution`，不是内部状态别名。

## 拒绝优先级

消息处理按以下顺序只返回第一个错误：

1. `from` 越界：`ErrSenderOutOfRange`
2. 摘要为空：`ErrEmptyDigest`
3. 视图不等于当前视图：`ErrWrongView`
4. 序号不在当前窗口：`ErrSeqOutOfWindow`
5. 角色违规：非主节点发送预准备返回 `ErrPrePrepareFromBackup`；主节点发送准备返回 `ErrPrepareFromPrimary`；未知消息类型返回 `ErrUnknownMessageKind`
6. 摘要冲突：预准备冲突返回 `ErrPrePrepareDigestClash`；准备冲突返回 `ErrPrepareDigestClash`；提交冲突返回 `ErrCommitDigestClash`

所有被拒绝的消息在校验完成前不写入状态。提交消息没有主节点角色限制。

## 不建模范围

当前实现不包含视图变更、检查点、稳定检查点垃圾回收、网络传输、请求负载执行、重发协议或拜占庭内容验证。它只复现固定视图内消息接受、证书计数和严格升序执行判定。

## 本地验证

```bash
go test -race -v ./pbft
go test ./...
go vet ./...
```

如果系统 Go 构建缓存目录不可写，可指定临时缓存：

```bash
GOCACHE=/tmp/go-cache go test -race -v ./pbft
```
