package lease

// 本文件不含测试代码，只承载"改坏方式 → 失败测试"对照审计表。
//
// 判别力保证：下列每一处都是实现里的真实关键判定。把左列那一行改成右列
// 描述的"坏版本"（其余不动），指定测试必须失败；恢复后必须全绿。
// 每条都已通过临时改坏实现、跑指定测试、再原样恢复的方式人工验证。
//
//  1. manager.go Acquire：
//     `if m.held && m.holder != holder && now < m.expiresAt`
//     改坏：删掉 `now < m.expiresAt`（过期也拒他人）
//     → TestOtherHolderCanAcquireExactlyAtExpiry 失败（B 在到期时刻拿不到）。
//
//  2. manager.go Acquire 同一判定：
//     `now < m.expiresAt` 改成 `now <= m.expiresAt`（边界算旧持有者有效）
//     → TestOtherHolderCanAcquireExactlyAtExpiry 失败；
//       这也会让"到期那一刻新旧持有者同时有效"的互斥漏洞通过测试。
//
//  3. manager.go Acquire：
//     `m.token++` 改成"同 holder 重复 Acquire 时不递增、直接返回旧 token"
//     → TestSameHolderReacquireIssuesNewTokenAndOldDies 失败
//       （tokNew == tokOld，且旧 token 写得到 nil 而非 ErrStaleToken）。
//
//  4. manager.go Release：
//     末尾增加计数器复位（如 `m.token = 0`）
//     → TestAcquireTokenStrictlyMonotonicAcrossReleaseAndExpiry 失败
//       （Release 后发出历史上出现过的 token，严格递增被破坏）。
//
//  5. manager.go Renew：
//     `if now >= m.expiresAt` 改成 `if now > m.expiresAt`
//     → TestRenewRejectedExactlyAtExpiry 失败（恰好到期续约成功）。
//
//  6. store.go Write：
//     `if now := m.now(); now >= m.expiresAt` 改成 `now > m.expiresAt`
//     → TestWriteRejectedExactlyAtExpiry 与
//       TestExpiredTokenWriteRejectedExactlyAndAfterExpiry 失败
//       （恰好到期的写成功，违反不变量 5）。
//
//  7. store.go Write：
//     `m.store[key] = val` 挪到过期校验之前（先写后判）
//     → TestWriteRejectedExactlyAtExpiry 与
//       TestRejectedWritesHaveNoSideEffectsAcrossKeys 失败
//       （不变量 4：被拒写留下了内容）。
//
//  8. manager.go Renew：
//     token 不符分支 `return ErrTokenMismatch` 换成 `return ErrNotHolder`
//     → TestHolderWithWrongTokenRenewGetsTokenMismatch 与
//       TestRenewSentinelsArePairwiseDistinct 失败。
//
//  9. manager.go Renew：
//     holder 不符分支 `return ErrNotHolder` 换成 `return ErrLeaseExpired`
//     → TestPreemptedHolderRenewGetsNotHolder 与
//       TestRenewSentinelsArePairwiseDistinct 失败。
//
// 10. manager.go Release：
//     删掉 `if token != m.token { return ErrTokenMismatch }`（旧 token 也能释放）
//     → TestReleaseWithWrongTokenGetsTokenMismatchAndKeepsLease 失败
//       （随后当前 token 的 Write 返回 ErrStaleToken）。
//
// 11. manager.go Release：
//     清记录前插入 `if m.now() >= m.expiresAt { return ErrLeaseExpired }`
//     （过期不允许幂等释放）
//     → TestReleaseExpiredMatchingLeaseSucceeds 失败。
//
// 12. store.go Write：
//     "前一个 token"分支单独返回新哨兵（如 ErrTokenMismatch）而非
//     ErrStaleToken
//     → TestWritePreviousTokenAndUnknownHugeTokenAreSameSentinel 失败
//       （破坏"前一个值与巨大值同类"的设计决策）。
//
// 13. manager.go / store.go：
//     删掉 m.mu.Lock/Unlock（或缩小临界区使 token++ 与记录更新非原子）
//     → TestConcurrentPreemptRenewWriteRelease 在 -race 下报数据竞争，
//       或 issued 出现重复 token / oracle diff。
