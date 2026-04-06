// Package permissions implements deny/allow matching for MCP tool calls.
//
// Rules have the form:
//
//	tool_name               — match all calls to this tool
//	tool_name:arg=value     — match when arg equals value
//	tool_name:arg=pattern*  — match wildcard
//	tool_name:*=pattern     — match any arg
package permissions

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config holds per-server permission rules loaded from permissions.json.
type Config map[string]ServerPolicy

// ServerPolicy defines the mode and rules for one MCP server.
type ServerPolicy struct {
	Mode  string   `json:"mode"`  // "deny" or "allow"
	Rules []string `json:"rules"` // rule patterns
}

// Load reads and parses permissions.json from the given path.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read permissions config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse permissions config: %w", err)
	}
	return cfg, nil
}

// Check evaluates whether a tool call is permitted.
// Returns true if the call is allowed, false if blocked.
//
// For "deny" mode: call is allowed unless a rule matches (deny-list).
// For "allow" mode: call is blocked unless a rule matches (allow-list).
func (p *ServerPolicy) Check(toolName string, args map[string]interface{}) bool {
	if p == nil {
		return true // no policy = allow all
	}

	matched := matchesAnyRule(toolName, args, p.Rules)

	switch p.Mode {
	case "deny":
		return !matched // deny mode: block if matched
	case "allow":
		return matched // allow mode: permit if matched
	default:
		return true // unknown mode = allow all
	}
}

// matchesAnyRule checks if any rule matches the tool call.
func matchesAnyRule(toolName string, args map[string]interface{}, rules []string) bool {
	for _, rule := range rules {
		if matchRule(toolName, args, rule) {
			return true
		}
	}
	return false
}

// matchRule evaluates a single rule against a tool call.
func matchRule(toolName string, args map[string]interface{}, rule string) bool {
	// Split into tool part and optional arg constraint
	parts := strings.SplitN(rule, ":", 2)
	ruleTool := parts[0]

	// Tool name must match
	if ruleTool != toolName {
		return false
	}

	// No arg constraint = match all calls to this tool
	if len(parts) == 1 {
		return true
	}

	// Parse arg constraint: arg=value or arg=pattern*
	constraint := parts[1]
	eqIdx := strings.Index(constraint, "=")
	if eqIdx < 0 {
		return false // malformed rule
	}

	argName := constraint[:eqIdx]
	argPattern := constraint[eqIdx+1:]

	// Wildcard arg name: *=pattern matches any arg
	if argName == "*" {
		for _, v := range args {
			if matchPattern(fmt.Sprint(v), argPattern) {
				return true
			}
		}
		return false
	}

	// Specific arg name
	v, ok := args[argName]
	if !ok {
		return false
	}

	return matchPattern(fmt.Sprint(v), argPattern)
}

// matchPattern does simple glob matching (only trailing * supported).
func matchPattern(value, pattern string) bool {
	if strings.HasSuffix(pattern, "*") {
		prefix := pattern[:len(pattern)-1]
		return strings.HasPrefix(value, prefix)
	}
	return value == pattern
}
