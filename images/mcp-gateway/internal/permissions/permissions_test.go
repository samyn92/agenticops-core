package permissions

import (
	"testing"
)

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		value   string
		pattern string
		want    bool
	}{
		{"hello", "hello", true},
		{"hello", "world", false},
		{"hello", "hel*", true},
		{"hello", "hello*", true},
		{"hello", "xyz*", false},
		{"", "*", true},
		{"anything", "*", true},
	}

	for _, tt := range tests {
		got := matchPattern(tt.value, tt.pattern)
		if got != tt.want {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.value, tt.pattern, got, tt.want)
		}
	}
}

func TestServerPolicyCheck(t *testing.T) {
	tests := []struct {
		name   string
		policy *ServerPolicy
		tool   string
		args   map[string]interface{}
		want   bool
	}{
		{
			name:   "nil policy allows all",
			policy: nil,
			tool:   "anything",
			want:   true,
		},
		{
			name:   "deny mode blocks matching tool",
			policy: &ServerPolicy{Mode: "deny", Rules: []string{"kubectl_exec"}},
			tool:   "kubectl_exec",
			want:   false,
		},
		{
			name:   "deny mode allows non-matching tool",
			policy: &ServerPolicy{Mode: "deny", Rules: []string{"kubectl_exec"}},
			tool:   "kubectl_get",
			want:   true,
		},
		{
			name:   "allow mode permits matching tool",
			policy: &ServerPolicy{Mode: "allow", Rules: []string{"kubectl_get", "kubectl_list"}},
			tool:   "kubectl_get",
			want:   true,
		},
		{
			name:   "allow mode blocks non-matching tool",
			policy: &ServerPolicy{Mode: "allow", Rules: []string{"kubectl_get"}},
			tool:   "kubectl_exec",
			want:   false,
		},
		{
			name:   "deny with arg constraint matches",
			policy: &ServerPolicy{Mode: "deny", Rules: []string{"kubectl_exec:namespace=kube-system"}},
			tool:   "kubectl_exec",
			args:   map[string]interface{}{"namespace": "kube-system"},
			want:   false,
		},
		{
			name:   "deny with arg constraint no match (different value)",
			policy: &ServerPolicy{Mode: "deny", Rules: []string{"kubectl_exec:namespace=kube-system"}},
			tool:   "kubectl_exec",
			args:   map[string]interface{}{"namespace": "default"},
			want:   true,
		},
		{
			name:   "deny with wildcard pattern",
			policy: &ServerPolicy{Mode: "deny", Rules: []string{"kubectl_exec:namespace=kube-*"}},
			tool:   "kubectl_exec",
			args:   map[string]interface{}{"namespace": "kube-system"},
			want:   false,
		},
		{
			name:   "deny with wildcard arg name",
			policy: &ServerPolicy{Mode: "deny", Rules: []string{"file_write:*=/etc/*"}},
			tool:   "file_write",
			args:   map[string]interface{}{"path": "/etc/passwd"},
			want:   false,
		},
		{
			name:   "deny with wildcard arg name no match",
			policy: &ServerPolicy{Mode: "deny", Rules: []string{"file_write:*=/etc/*"}},
			tool:   "file_write",
			args:   map[string]interface{}{"path": "/home/user/file.txt"},
			want:   true,
		},
		{
			name:   "allow with multiple rules",
			policy: &ServerPolicy{Mode: "allow", Rules: []string{"kubectl_get", "kubectl_list", "kubectl_describe"}},
			tool:   "kubectl_describe",
			want:   true,
		},
		{
			name:   "missing arg in constraint",
			policy: &ServerPolicy{Mode: "deny", Rules: []string{"tool:arg=value"}},
			tool:   "tool",
			args:   map[string]interface{}{},
			want:   true, // arg not present, rule doesn't match, deny doesn't fire
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.args
			if args == nil {
				args = map[string]interface{}{}
			}
			got := tt.policy.Check(tt.tool, args)
			if got != tt.want {
				t.Errorf("Check(%q, %v) = %v, want %v", tt.tool, args, got, tt.want)
			}
		})
	}
}
