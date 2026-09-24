# NOTES — as-of 时间旅行读
前置：W(a,1)=1, W(b,10)=2, W(a,2)=3。Compact(2)：a 留基线 v1(seq1)+活 v3(seq3)；b 最新版 v1(seq2)≤2，整体降为基线。
1. AsOf(0)：无 seq≤0 版本 → {}
2. AsOf(1)：a 取 v1(seq1)；b 唯一 v1(seq2)>1 不取 → {a:1}
3. AsOf(2)：a 的 v3(seq3)>2 取 v1；b 的 v1(seq2)=2，等号即可见 → {a:1,b:10}
4. AsOf(3)：a 取 v3(seq3,val2)；b 取 v1(seq2) → {a:2,b:10}
5. AsOf(4)：超过最新 Seq，收敛按 3 处理 → {a:2,b:10}
6. Compact(2) 后 AsOf(2)：2≤upto(2) → ErrCompacted，绝不返回旧数据
7. Compact(2) 后 AsOf(3)：a 取活 v3；b 取基线 v1(seq2≤3) → {a:2,b:10}（b 仍在）
8. Compact(2) 后 AsOf(4)：收敛最新 → {a:2,b:10}
（甲）b 在 AsOf(2) 可见，边界是 seq≤s 含等号。若错写成 seq<s：AsOf(2) 错成 {a:1}（b 的 seq2 被排除）；AsOf(1) 错成 {}（a 的 seq1 也被排除，正确应为 {a:1}）。
（乙）视图里仍有 b=10，它是被保留的基线版本。若 seq≤upto 全部丢弃不留基线，AsOf(3) 错成 {a:2}，b 凭空消失，违反不变量2；若继续正常服务 AsOf(2) 返回 {a:1,b:10}，则违反不变量3。
（丙）Compact(3)：a 最新版 v3(seq3)≤3 整体降为基线（基线取值 val=2），b 基线 v1；AsOf(3)=ErrCompacted（3≤3），AsOf(4)={a:2,b:10}。s==upto 若仍可达，直接违反不变量3：回收边界必须含等号，「不可达」才有确定性。
不变量1（与朴素重放一致）：api.go SelfCheck 内置按 Seq 升序重放、逐 key 比对；钉于 TestSelfCheck、TestNaiveReplayRandom。
不变量2（Compact 不破坏可达读）：store.go Compact 收基线、hist.go At 从基线/活版本定位；钉于 TestCompactPreserves。
不变量3（不可达即报错）：store.go AsOf 开头判定 s≤compacted 即返回 ErrCompacted；钉于 TestReachability。
不变量4（失败不留痕）：store.go 的 Write/AsOf/Compact 全部先校验参数、通过后才分配 Seq 或改链；钉于 TestRejectedNoTrace。
复杂度：hist.go 非导出字段 probe 记最近一次 At 检查的版本数，读最新位点直取活链末元素（1 次）；钉于 hist_test.go TestProbeConstant（m=100…10000，穿插其他 key）。
