package harness

import "slices"

type fileVersion struct {
	child    string
	sequence int64
	before   string
	hash     string
	change   bool
}

// Same-child saved order or explicit DAG order can establish a version chain.
// A matching hash alone does not prove that independent branches were isolated.
func fileConflicts(plan CollaborationPlan, versions map[string][]fileVersion) []EvidenceConflict {
	dependencies := map[string][]string{}
	for _, child := range plan.Children {
		dependencies[child.Name] = child.DependsOn
	}
	var precedes func(string, string) bool
	precedes = func(before, after string) bool {
		for _, dependency := range dependencies[after] {
			if dependency == before || precedes(before, dependency) {
				return true
			}
		}
		return false
	}
	ordered := func(before, after fileVersion) bool {
		return before.sequence < after.sequence && (before.child == after.child || precedes(before.child, after.child))
	}
	var paths []string
	for path := range versions {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	var conflicts []EvidenceConflict
	for _, path := range paths {
		events := slices.Clone(versions[path])
		slices.SortStableFunc(events, func(a, b fileVersion) int {
			if a.sequence < b.sequence {
				return -1
			}
			if a.sequence > b.sequence {
				return 1
			}
			return 0
		})
		current, conflict := "", false
		var previous []fileVersion
		var children, hashes []string
		for _, event := range events {
			if !slices.Contains(children, event.child) {
				children = append(children, event.child)
			}
			for _, hash := range []string{event.before, event.hash} {
				if hash != "" && !slices.Contains(hashes, hash) {
					hashes = append(hashes, hash)
				}
			}
			if len(previous) != 0 {
				expected := event.hash
				if event.change {
					expected = event.before
				}
				if expected != current {
					conflict = true
				}
				if event.change && event.hash != current {
					for _, prior := range previous {
						if !ordered(prior, event) {
							conflict = true
						}
					}
				}
			}
			if event.change && event.hash != current {
				previous = nil
			}
			current = event.hash
			previous = append(previous, event)
		}
		if conflict {
			conflicts = append(conflicts, EvidenceConflict{Path: path, Children: children, Hashes: hashes})
		}
	}
	return conflicts
}
