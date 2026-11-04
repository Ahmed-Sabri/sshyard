package migration

func resolveReferences(r *Result) {
	ids := map[string][]int{}
	for i, e := range r.Entries {
		if e.ID != "" {
			ids[e.ID] = append(ids[e.ID], i)
		}
	}
	for i := range r.Entries {
		e := &r.Entries[i]
		if len(ids[e.ID]) > 1 {
			issue(e, "duplicate-id", "id")
		}
		for j := range e.Jumps {
			h := &e.Jumps[j]
			if h.Ref == "" {
				probe := Entry{Name: e.Name, Address: h.Address, Port: h.Port}
				validateEntry(&probe)
				h.Address = probe.Address
				h.Port = probe.Port
				e.Issues = append(e.Issues, probe.Issues...)
				continue
			}
			matches := ids[h.Ref]
			if len(matches) != 1 {
				issue(e, "missing-reference", "jump")
				continue
			}
			ref := r.Entries[matches[0]]
			h.Name = ref.Name
			h.Address = ref.Address
			h.User = ref.User
			h.Port = ref.Port
		}
		if referenceCycle(r.Entries, ids, i) {
			issue(e, "invalid-entry", "jump")
		}
	}
}

// referenceCycle uses a bounded explicit stack, including cycles below the root.
func referenceCycle(entries []Entry, ids map[string][]int, root int) bool {
	type cursor struct{ index, next int }
	stack := []cursor{{index: root}}
	active := map[int]bool{root: true}
	done := map[int]bool{}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.next >= len(entries[top.index].Jumps) {
			done[top.index] = true
			delete(active, top.index)
			stack = stack[:len(stack)-1]
			continue
		}
		hop := entries[top.index].Jumps[top.next]
		top.next++
		matches := ids[hop.Ref]
		if len(matches) != 1 {
			continue
		}
		index := matches[0]
		if active[index] {
			return true
		}
		if done[index] {
			continue
		}
		active[index] = true
		stack = append(stack, cursor{index: index})
	}
	return false
}
