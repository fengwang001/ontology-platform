package replication

import (
	"fmt"
)

// recoverFollower 是 RecoverFollower 在已校验副本名后的内部实现。
//
// 每轮取跟随者缓存中的最新世代 G，向领导者询问 G 的可用结束位点：
//   - 领导者没有 G：删除 G 的缓存项，并把日志截到 G 的起始位点（只删分叉）；
//   - 领导者有 G：逐条比较重叠段，发现分歧即截到分歧位点，完全一致即结束。
//
// 全程只截断、不拉取；截断点最终等于与领导者日志的最长公共前缀长度。
func recoverFollower(c *Cluster, leader, follower *Replica) (int, error) {
	log := c.logger
	log.Info("recover start", "leader", leader.name, "follower", follower.name,
		"follower_len", follower.len(), "cache_len", len(follower.gen),
		"reason", "latest generation of follower will be probed against the leader round by round")

	for round := 1; ; round++ {
		if follower.len() == 0 {
			log.Info("recover round", "round", round, "follower", follower.name,
				"cut", 0, "reason", "follower log already empty; longest common prefix is 0")
			return 0, nil
		}
		mark := follower.gen[len(follower.gen)-1]
		end, err := generationEndAtLocked(leader, mark.Gen, mark.Start)
		if err != nil {
			cut := mark.Start
			log.Info("recover round", "round", round, "follower", follower.name,
				"gen", mark.Gen, "leader_has_gen", false,
				"cut", cut, "cut_len", follower.len()-cut,
				"reason", fmt.Sprintf("leader has no generation %d; drop its cache entry and cut to its start %d",
					mark.Gen, cut))
			follower.truncateAt(cut)
			continue
		}

		limit := end
		if follower.len() < limit {
			limit = follower.len()
		}
		cut := -1
		for i := mark.Start; i < limit; i++ {
			le, _ := leader.entryAt(i)
			fe, _ := follower.entryAt(i)
			if le != fe {
				cut = i
				break
			}
		}
		if cut < 0 {
			if follower.len() > end {
				cut = end
				log.Info("recover round", "round", round, "follower", follower.name,
					"gen", mark.Gen, "leader_has_gen", true, "gen_end", end,
					"cut", cut, "cut_len", follower.len()-cut,
					"reason", fmt.Sprintf("shared generation %d matched but follower has extra divergent tail beyond leader end %d",
						mark.Gen, end))
				follower.truncateAt(cut)
				continue
			}
			log.Info("recover done", "rounds", round, "follower", follower.name,
				"cut", follower.len(), "follower_len", follower.len(),
				"reason", fmt.Sprintf("generation %d range [%d,%d) matches leader; remaining tail is the longest common prefix",
					mark.Gen, mark.Start, end))
			return follower.len(), nil
		}

		log.Info("recover round", "round", round, "follower", follower.name,
			"gen", mark.Gen, "leader_has_gen", true, "gen_end", end,
			"cut", cut, "cut_len", follower.len()-cut,
			"reason", fmt.Sprintf("first mismatch in generation %d at position %d", mark.Gen, cut))
		follower.truncateAt(cut)
	}
}
