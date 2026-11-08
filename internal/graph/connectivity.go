package graph

// ForwardSize counts the nodes reachable from src, stopping once the count
// passes limit. A snapped start that reaches only a pocket of a few hundred
// nodes expands almost nothing in A*, so the caller uses this to tell a dead
// start from a road that leads somewhere before spending a search on it.
func (g *Graph) ForwardSize(src uint32, limit int) int {
	if src >= g.NodeCount || limit < 1 {
		return limit + 1
	}
	seen := make(map[uint32]struct{}, limit+1)
	stack := make([]uint32, 0, limit+1)
	seen[src] = struct{}{}
	stack = append(stack, src)
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for e := g.Off[u]; e < g.Off[u+1]; e++ {
			v := g.To[e]
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			if len(seen) > limit {
				return len(seen)
			}
			stack = append(stack, v)
		}
	}
	return len(seen)
}
