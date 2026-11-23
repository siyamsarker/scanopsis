// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package findings

import "testing"

func TestGitLabLink(t *testing.T) {
	deref := func(p *string) string {
		if p == nil {
			return "<nil>"
		}
		return *p
	}
	tests := []struct {
		base, path, kind, id, want string
	}{
		{"https://gitlab.example.com/", "platform/payments-api", "jobs", "42", "https://gitlab.example.com/platform/payments-api/-/jobs/42"},
		{"https://gitlab.example.com", "a b/c?d#e", "jobs", "1", "https://gitlab.example.com/a%20b/c%3Fd%23e/-/jobs/1"},
		{"https://gitlab.example.com", "../../evil.com", "jobs", "1", "https://gitlab.example.com/../../evil.com/-/jobs/1"},
		{"https://gitlab.example.com", "group/app", "commit", "a/b?c", "https://gitlab.example.com/group/app/-/commit/a%2Fb%3Fc"},
		{"", "group/app", "jobs", "1", "<nil>"},
		{"https://gitlab.example.com", "group/app", "commit", "", "<nil>"},
	}
	for _, tt := range tests {
		if got := deref(gitlabLink(tt.base, tt.path, tt.kind, tt.id)); got != tt.want {
			t.Errorf("gitlabLink(%q, %q, %q, %q) = %s, want %s", tt.base, tt.path, tt.kind, tt.id, got, tt.want)
		}
	}
}

func TestCommitID(t *testing.T) {
	for sha, want := range map[string]string{
		"0123456789abcdef0123456789abcdef01234567": "0123456789abcdef0123456789abcdef01234567",
		"abc1234":   "abc1234",
		"abc123":    "",
		"ABC1234":   "",
		"abc1234/x": "",
		"":          "",
	} {
		if got := commitID(sha); got != want {
			t.Errorf("commitID(%q) = %q, want %q", sha, got, want)
		}
	}
}
