package harness

import (
	"errors"
	"slices"
	"sort"
)

type CapabilityItem struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

type CapabilityCatalog struct {
	Skills     []CapabilityItem `json:"skills"`
	MCPServers []CapabilityItem `json:"mcp_servers"`
	Extensions []CapabilityItem `json:"extensions"`
}

func capabilityCatalog(config Config) CapabilityCatalog {
	catalog := CapabilityCatalog{
		Skills:     make([]CapabilityItem, 0, len(config.Skills.Enabled)),
		MCPServers: make([]CapabilityItem, 0, len(config.MCPServers)),
		Extensions: make([]CapabilityItem, 0, len(config.Extensions.Enabled)),
	}
	for id, enabled := range config.Skills.Enabled {
		catalog.Skills = append(catalog.Skills, CapabilityItem{ID: id, Enabled: enabled})
	}
	for id, server := range config.MCPServers {
		catalog.MCPServers = append(catalog.MCPServers, CapabilityItem{ID: id, Enabled: server.Enabled})
	}
	for id, enabled := range config.Extensions.Enabled {
		catalog.Extensions = append(catalog.Extensions, CapabilityItem{ID: id, Enabled: enabled})
	}
	sort.Slice(catalog.Skills, func(i, j int) bool { return catalog.Skills[i].ID < catalog.Skills[j].ID })
	sort.Slice(catalog.MCPServers, func(i, j int) bool { return catalog.MCPServers[i].ID < catalog.MCPServers[j].ID })
	sort.Slice(catalog.Extensions, func(i, j int) bool { return catalog.Extensions[i].ID < catalog.Extensions[j].ID })
	return catalog
}

func (tasks *TaskService) CapabilityCatalog() CapabilityCatalog {
	if tasks == nil || tasks.core == nil {
		return CapabilityCatalog{}
	}
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	catalog := capabilityCatalog(tasks.core.currentConfig())
	registry, err := scanSkills(tasks.core.Store.DataDir(), tasks.core.currentConfig().Workspace, tasks.core.currentConfig())
	if err != nil {
		return catalog
	}
	known := make(map[string]bool, len(catalog.Skills))
	for _, item := range catalog.Skills {
		known[item.ID] = true
	}
	for name := range registry.Skills {
		if !known[name] {
			catalog.Skills = append(catalog.Skills, CapabilityItem{ID: name, Enabled: tasks.core.currentConfig().Skills.Enabled[name]})
		}
	}
	sort.Slice(catalog.Skills, func(i, j int) bool { return catalog.Skills[i].ID < catalog.Skills[j].ID })
	return catalog
}

// CapabilitySelection is a compact, turn-local selection. It is stored inside
// the existing run snapshot rather than becoming another persisted object.
type CapabilitySelection struct {
	Skills     []string `json:"skills,omitempty"`
	MCPServers []string `json:"mcp_servers,omitempty"`
	Extensions []string `json:"extensions,omitempty"`
}

func (selection CapabilitySelection) normalize() CapabilitySelection {
	selection.Skills = uniqueSorted(selection.Skills)
	selection.MCPServers = uniqueSorted(selection.MCPServers)
	selection.Extensions = uniqueSorted(selection.Extensions)
	return selection
}

func (selection CapabilitySelection) validate(config Config) error {
	selection = selection.normalize()
	if len(selection.Skills) > 256 || len(selection.MCPServers) > 16 || len(selection.Extensions) > 256 {
		return errors.New("harness: too many enabled turn capabilities")
	}
	for _, name := range selection.Skills {
		if !config.Skills.Enabled[name] {
			return errors.New("harness: selected skill is not enabled")
		}
	}
	for _, name := range selection.MCPServers {
		server, ok := config.MCPServers[name]
		if !ok || !server.Enabled {
			return errors.New("harness: selected MCP server is not enabled")
		}
	}
	for _, name := range selection.Extensions {
		if !config.Extensions.Enabled[name] {
			return errors.New("harness: selected extension is not enabled")
		}
	}
	return nil
}

func defaultCapabilities(config Config) CapabilitySelection {
	selection := CapabilitySelection{}
	for name, enabled := range config.Skills.Enabled {
		if enabled {
			selection.Skills = append(selection.Skills, name)
		}
	}
	for name, server := range config.MCPServers {
		if server.Enabled {
			selection.MCPServers = append(selection.MCPServers, name)
		}
	}
	for name, enabled := range config.Extensions.Enabled {
		if enabled {
			selection.Extensions = append(selection.Extensions, name)
		}
	}
	return selection.normalize()
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	values = slices.Clone(values)
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		if value == "" || len(result) != 0 && result[len(result)-1] == value {
			continue
		}
		result = append(result, value)
	}
	return result
}
