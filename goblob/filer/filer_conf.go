package filer

import (
	"sort"
)

// FilerConf holds path-based configuration rules for the filer.
type FilerConf struct {
	Rules map[string]*FilerConfRule
}

// FilerConfRule defines configuration for a path prefix.
type FilerConfRule struct {
	PathPrefix   string
	Collection   string
	Replication  string
	Ttl          string
	DiskType     string
	MaxMB        int
}

// NewFilerConf creates a new FilerConf.
func NewFilerConf() *FilerConf {
	return &FilerConf{
		Rules: make(map[string]*FilerConfRule),
	}
}

// MatchRule finds the longest matching rule for a given path.
func (fc *FilerConf) MatchRule(path FullPath) *FilerConfRule {
	pathStr := string(path)

	// Collect matching rules
	var matches []FilerConfRule
	for _, rule := range fc.Rules {
		if len(pathStr) >= len(rule.PathPrefix) && pathStr[:len(rule.PathPrefix)] == rule.PathPrefix {
			matches = append(matches, *rule)
		}
	}

	// Sort by prefix length (longest first)
	sort.Slice(matches, func(i, j int) bool {
		return len(matches[i].PathPrefix) > len(matches[j].PathPrefix)
	})

	// Return longest match
	if len(matches) > 0 {
		return &matches[0]
	}

	// Return default rule
	return &FilerConfRule{
		PathPrefix:  "/",
		Collection:  "",
		Replication: "000",
		Ttl:         "",
		DiskType:    "",
		MaxMB:       0,
	}
}

// AddRule adds a new rule to the configuration.
func (fc *FilerConf) AddRule(rule *FilerConfRule) {
	fc.Rules[rule.PathPrefix] = rule
}

// GetRule returns a rule by path prefix.
func (fc *FilerConf) GetRule(pathPrefix string) (*FilerConfRule, bool) {
	rule, ok := fc.Rules[pathPrefix]
	return rule, ok
}

// RemoveRule removes a rule by path prefix.
func (fc *FilerConf) RemoveRule(pathPrefix string) {
	delete(fc.Rules, pathPrefix)
}
