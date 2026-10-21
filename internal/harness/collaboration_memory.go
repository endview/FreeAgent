package harness

import (
	"slices"
)

// These are task-local capabilities, not organization or role registry state.
type SharedMemoryAccess struct {
	Read    bool     `json:"read"`
	Append  bool     `json:"append"`
	Modify  bool     `json:"modify"`
	Kinds   []string `json:"kinds,omitempty"`
	Entries []string `json:"entries,omitempty"`
}

type RoleMemory struct {
	AgentID   string             `json:"agent_id,omitempty"`
	Selection MemorySelection    `json:"selection"`
	Shared    SharedMemoryAccess `json:"shared"`
}

func (access SharedMemoryAccess) enabled() bool {
	return access.Read || access.Append || access.Modify
}

func (access SharedMemoryAccess) grant(parent, project string) *MemoryGrant {
	if !access.enabled() {
		return nil
	}
	kinds := slices.Clone(access.Kinds)
	if len(kinds) == 0 {
		kinds = []string{"fact", "hypothesis", "finding", "decision", "question", "artifact"}
	}
	return &MemoryGrant{Scope: MemoryScope{Kind: "shared", Owner: parent, Project: project},
		Read: access.Read, Append: access.Append, Modify: access.Modify,
		Kinds: kinds, Entries: slices.Clone(access.Entries)}
}

func (access SharedMemoryAccess) validate(project string) error {
	if !access.enabled() {
		if len(access.Kinds)+len(access.Entries) != 0 {
			return ErrMemoryDenied
		}
		return nil
	}
	policy := memoryRunPolicy{AgentID: "validation", ParentID: "validation",
		Shared: access.grant("validation", project)}
	return policy.validate(project)
}

func (memory RoleMemory) policy(parent, project, agent string) (*memoryRunPolicy, error) {
	if memory.AgentID != "" {
		agent = memory.AgentID
	}
	if agent == "" {
		agent = "expert-" + newID()
	}
	policy := &memoryRunPolicy{AgentID: agent, ParentID: parent,
		Selection: memory.Selection, Shared: memory.Shared.grant(parent, project)}
	return policy, policy.validate(project)
}

func (access SharedMemoryAccess) within(authorized SharedMemoryAccess) bool {
	if access.Read && !authorized.Read || access.Append && !authorized.Append || access.Modify && !authorized.Modify {
		return false
	}
	if !access.enabled() {
		return len(access.Kinds)+len(access.Entries) == 0
	}
	if len(authorized.Kinds) != 0 {
		if len(access.Kinds) == 0 {
			return false
		}
		for _, kind := range access.Kinds {
			if !slices.Contains(authorized.Kinds, kind) {
				return false
			}
		}
	}
	if len(authorized.Entries) != 0 {
		if len(access.Entries) == 0 {
			return false
		}
		for _, id := range access.Entries {
			if !slices.Contains(authorized.Entries, id) {
				return false
			}
		}
	}
	return true
}
