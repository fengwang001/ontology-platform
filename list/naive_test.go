package list

import (
	"fmt"
	"sort"
	"strings"

	"ontology/authz"
	"ontology/keyindex"
)

func authzNormalize(gs []string) []string {
	return authz.Normalize(gs)
}

func naiveSorted(data []keyindex.KeyVersions) []keyindex.KeyVersions {
	keys := append([]keyindex.KeyVersions(nil), data...)
	sort.Slice(keys, func(i, j int) bool { return keys[i].Key < keys[j].Key })
	for i := range keys {
		sort.Slice(keys[i].Versions, func(a, b int) bool {
			return keys[i].Versions[a].ID > keys[i].Versions[b].ID
		})
	}
	return keys
}

type visibleGrants []string

func (g visibleGrants) visible(key string) bool {
	return authz.Grants(g).Visible(key)
}

// naiveEntry 是朴素扫描结果的扁平条目。
type naiveEntry struct {
	isPrefix bool
	key      string
	version  int64
	isDelete bool
}

func (e naiveEntry) same(o Entry) bool {
	return e.isPrefix == o.IsPrefix && e.key == o.Key &&
		(e.isPrefix || e.version == o.Version && e.isDelete == o.IsDelete)
}

// naiveScan 按规格逐键朴素扫描，一次性返回全部条目（不做寻址优化）。
func naiveScan(data []keyindex.KeyVersions, req Request) []naiveEntry {
	grants := authz.Normalize(req.Grants)
	keys := append([]keyindex.KeyVersions(nil), data...)
	sort.Slice(keys, func(i, j int) bool { return keys[i].Key < keys[j].Key })
	for i := range keys {
		sort.Slice(keys[i].Versions, func(a, b int) bool {
			return keys[i].Versions[a].ID > keys[i].Versions[b].ID
		})
	}

	delim := ""
	if len(req.Delimiter) == 1 {
		delim = req.Delimiter
	}
	var out []naiveEntry
	groups := map[string]bool{}

	emitKey := func(kv keyindex.KeyVersions) {
		if !grants.Visible(kv.Key) {
			return
		}
		if !strings.HasPrefix(kv.Key, req.Prefix) {
			return
		}
		rest := kv.Key[len(req.Prefix):]
		if delim != "" && strings.Contains(rest, delim) {
			i := strings.IndexByte(rest, delim[0])
			g := req.Prefix + rest[:i+1]
			if !groups[g] {
				groups[g] = true
				out = append(out, naiveEntry{isPrefix: true, key: g})
			}
			return
		}
		if req.Kind == Objects {
			if len(kv.Versions) > 0 && kv.Versions[0].IsDelete {
				return
			}
			out = append(out, naiveEntry{key: kv.Key})
		} else {
			vs := append([]keyindex.Version(nil), kv.Versions...)
			sort.Slice(vs, func(i, j int) bool { return vs[i].ID > vs[j].ID })
			for _, v := range vs {
				out = append(out, naiveEntry{key: kv.Key, version: v.ID, isDelete: v.IsDelete})
			}
		}
	}

	if req.Kind == Objects {
		for _, kv := range keys {
			if kv.Key > req.StartAfter {
				emitKey(kv)
			}
		}
	} else {
		for _, kv := range keys {
			if kv.Key > req.StartAfter {
				emitKey(kv)
			}
		}
	}
	return out
}

// paginate 用服务端把请求走完所有页，返回拼接条目与每页截断标记。
func paginate(srv *Server, req Request) ([]naiveEntry, []bool, error) {
	var got []naiveEntry
	var trunc []bool
	for page := 0; ; page++ {
		var resp Response
		var err error
		if req.Kind == Versions {
			resp, err = srv.ListVersions(req)
		} else {
			resp, err = srv.ListObjects(req)
		}
		if err != nil {
			return nil, nil, err
		}
		for _, e := range resp.Entries {
			got = append(got, naiveEntry{
				isPrefix: e.IsPrefix,
				key:      e.Key,
				version:  e.Version,
				isDelete: e.IsDelete,
			})
		}
		trunc = append(trunc, resp.IsTruncated)
		if !resp.IsTruncated {
			if page > 20000 {
				return nil, nil, fmt.Errorf("too many pages")
			}
			break
		}
		req.Token = resp.NextToken
		req.StartAfter = ""
	}
	return got, trunc, nil
}
