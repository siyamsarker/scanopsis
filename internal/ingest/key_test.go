// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"strings"
	"testing"
)

// keyCase is one artifact name and the key it gets. The same cases are run
// against the key rules of migration 00011 (key_integration_test.go), so the
// keys of new uploads and of filled-in older scans always agree.
type keyCase struct {
	kind, name, project, want string
}

var keyCases = []keyCase{
	// Images: the tag and the digest are not part of the key.
	{KindImage, "web-frontend-stage:2", "demo/web-frontend", "web-frontend-stage"},
	{KindImage, "web-frontend-stage:3", "demo/web-frontend", "web-frontend-stage"},
	{KindImage, "web-frontend-stage", "demo/web-frontend", "web-frontend-stage"},
	{KindImage, "registry:5000/team/app:1", "demo/web-frontend", "registry:5000/team/app"},
	{KindImage, "registry:5000/team/tool", "demo/web-frontend", "registry:5000/team/tool"},
	{KindImage, "app@sha256:0d2b1e8f", "demo/web-frontend", "app"},
	{KindImage, "Registry.example.com/Team/Worker:1.2@sha256:0d2b1e8f", "demo/web-frontend", "Registry.example.com/Team/Worker"},
	{KindImage, "registry.example.com/team/app:1:2", "demo/web-frontend", "registry.example.com/team/app"},
	{KindImage, "registry.example.com/team/app@sha256:aa/bb:1", "demo/web-frontend", "registry.example.com/team/app"},
	{KindImage, "registry.example.com/team/app:1/", "demo/web-frontend", "registry.example.com/team/app:1/"},
	{KindImage, "/builds/x/0/demo/web-frontend", "demo/web-frontend", "/builds/x/0/demo/web-frontend"},
	{KindImage, "ünïcode/äpp:1", "demo/web-frontend", "ünïcode/äpp"},
	// A name the rules would empty is its own key.
	{KindImage, ":1", "demo/web-frontend", ":1"},
	{KindImage, "@sha256:0d2b1e8f", "demo/web-frontend", "@sha256:0d2b1e8f"},
	{KindImage, "", "demo/web-frontend", ""},

	// Repository scans: the runner's build folder is the repository.
	{KindSecretFS, ".", "demo/web-frontend", "."},
	{KindSecretFS, "services/web", "demo/web-frontend", "services/web"},
	{KindSecretFS, "/builds/x/0/demo/web-frontend", "demo/web-frontend", "."},
	{KindSecretFS, "/builds/x/0/demo/web-frontend/", "demo/web-frontend", "."},
	{KindSecretFS, "/builds/x/0/demo/web-frontend/services/api", "demo/web-frontend", "services/api"},
	{KindSecretFS, "/builds/x/0/demo/web-frontend/services/api/", "demo/web-frontend", "services/api"},
	{KindSecretFS, "/builds/x/0/demo/web-frontend//", "demo/web-frontend", "."},
	{KindSecretFS, "/builds/x/0/Demo/Web-Frontend/Services/API", "demo/web-frontend", "Services/API"},
	{KindSecretFS, "/builds/x/0/demo/web-frontend", "Demo/Web-Frontend", "."},
	{KindSecretFS, "/builds/x/0/demo/web-frontend-old/services/api", "demo/web-frontend", "/builds/x/0/demo/web-frontend-old/services/api"},
	{KindSecretFS, "/builds/x/0/demo/web-frontend/services/api", "demo/payments-api", "/builds/x/0/demo/web-frontend/services/api"},
	{KindSecretFS, "/srv/other", "demo/web-frontend", "/srv/other"},
	// The path seen first decides, the end of the name before any other.
	{KindSecretFS, "/a/demo/web-frontend/b/demo/web-frontend/c", "demo/web-frontend", "b/demo/web-frontend/c"},
	{KindSecretFS, "/a/demo/web-frontend/b/demo/web-frontend", "demo/web-frontend", "."},
	// Characters whose lower case is longer or shorter in UTF-8 (İ, Ⱥ, ẞ)
	// do not move the remainder.
	{KindSecretFS, "/builds/İstanbul/Ⱥ/demo/web-frontend/sérvices/ẞapi", "demo/web-frontend", "sérvices/ẞapi"},
	{KindSecretFS, "/builds/İİİİ/Demo/Web-Frontend", "demo/web-frontend", "."},
	{KindSecretFS, "/builds/ü/demo/web-frontendİ/x", "demo/web-frontend", "/builds/ü/demo/web-frontendİ/x"},
	{KindSecretFS, "", "demo/web-frontend", ""},
}

func TestArtifactKey(t *testing.T) {
	for _, tc := range keyCases {
		got, err := ArtifactKey(tc.kind, tc.name, tc.project, "")
		if err != nil || got != tc.want {
			t.Errorf("ArtifactKey(%q, %q, %q) = %q, %v; want %q", tc.kind, tc.name, tc.project, got, err, tc.want)
		}
	}
}

func TestArtifactKeyOverride(t *testing.T) {
	for _, tc := range []struct{ kind, name, override string }{
		{KindImage, "web-frontend-stage:4", "web-frontend-stage-amd64"},
		{KindSecretFS, "/builds/x/0/demo/web-frontend", "repository"},
		{KindImage, "app:1", "Bäckend api: one/two @3"},
		{KindImage, "app:1", strings.Repeat("ü", 200)},
	} {
		got, err := ArtifactKey(tc.kind, tc.name, "demo/web-frontend", tc.override)
		if err != nil || got != tc.override {
			t.Errorf("override %q: got %q, %v", tc.override, got, err)
		}
	}
	for _, bad := range []string{
		strings.Repeat("a", 201),
		strings.Repeat("ü", 201),
		"a\x00b", "a\tb", "a\nb", "a\rb", "a\x7fb", "a\u0085b", "a\u200bb", "a\u202eb",
		"\xff\xfe", " ", "   ",
	} {
		if got, err := ArtifactKey(KindImage, "app:1", "demo/web-frontend", bad); err == nil {
			t.Errorf("override %q accepted as %q", bad, got)
		}
	}
}

// targetCase is the target a vulnerability of a scan is stored with. The same
// cases are run against migration 00011 (key_integration_test.go).
type targetCase struct {
	kind, name, key, target, want string
}

var targetCases = []targetCase{
	// Trivy names the OS packages of an image "<artifact name> (<os> <version>)".
	{KindImage, "web-frontend-stage:3", "web-frontend-stage", "web-frontend-stage:3 (alpine 3.20.3)", "web-frontend-stage (alpine 3.20.3)"},
	{KindImage, "web-frontend-stage:3", "web-frontend-stage", "web-frontend-stage:3", "web-frontend-stage"},
	{KindImage, "app@sha256:0d2b1e8f", "app", "app@sha256:0d2b1e8f (debian 12.8)", "app (debian 12.8)"},
	{KindImage, "registry:5000/team/app:1", "registry:5000/team/app", "registry:5000/team/app:1 (ubuntu 22.04)", "registry:5000/team/app (ubuntu 22.04)"},
	{KindImage, "ünïcode/äpp:1", "ünïcode/äpp", "ünïcode/äpp:1 (alpine 3.20.3)", "ünïcode/äpp (alpine 3.20.3)"},
	// An artifact named by the upload.
	{KindImage, "web-frontend-stage:4", "web-frontend-stage-amd64", "web-frontend-stage:4 (alpine 3.20.3)", "web-frontend-stage-amd64 (alpine 3.20.3)"},
	// Anything else is kept.
	{KindImage, "web-frontend-stage:3", "web-frontend-stage", "app/package-lock.json", "app/package-lock.json"},
	{KindImage, "web-frontend-stage:3", "web-frontend-stage", "Python", "Python"},
	{KindImage, "web-frontend-stage:3", "web-frontend-stage", "web-frontend-stage:30 (alpine 3.20.3)", "web-frontend-stage:30 (alpine 3.20.3)"},
	{KindImage, "web-frontend-stage:3", "web-frontend-stage", "web-frontend-stage:3(alpine 3.20.3)", "web-frontend-stage:3(alpine 3.20.3)"},
	{KindImage, "web-frontend-stage:3", "web-frontend-stage", "Web-frontend-stage:3 (alpine 3.20.3)", "Web-frontend-stage:3 (alpine 3.20.3)"},
	{KindImage, "web-frontend-stage:3", "web-frontend-stage", "web-frontend-stage (alpine 3.20.3)", "web-frontend-stage (alpine 3.20.3)"},
	{KindImage, "registry:5000/team/tool", "registry:5000/team/tool", "registry:5000/team/tool (debian 12.8)", "registry:5000/team/tool (debian 12.8)"},
	{KindImage, "", "", " (debian 12.8)", " (debian 12.8)"},
	{KindImage, "", "", "", ""},
	{KindSecretFS, "/builds/x/0/demo/web-frontend", ".", "/builds/x/0/demo/web-frontend (debian 12.8)", "/builds/x/0/demo/web-frontend (debian 12.8)"},
}

func TestVulnTarget(t *testing.T) {
	for _, tc := range targetCases {
		if got := vulnTarget(tc.kind, tc.name, tc.key, tc.target); got != tc.want {
			t.Errorf("vulnTarget(%q, %q, %q, %q) = %q, want %q", tc.kind, tc.name, tc.key, tc.target, got, tc.want)
		}
	}
	// A longer name given by the upload keeps the target within its limit.
	long := strings.Repeat("ü", 200)
	target := "app:1 (" + strings.Repeat("x", maxFieldLen-8) + ")"
	got := vulnTarget(KindImage, "app:1", long, target)
	if len(got) > maxFieldLen || !strings.HasPrefix(got, long+" (xxx") {
		t.Errorf("long target: %d bytes, %.20q", len(got), got)
	}
}

// The answer to a bad artifact parameter names the rules people trip over:
// non-ASCII spaces (a pasted no-break space) and a value of only spaces.
func TestArtifactParamMessage(t *testing.T) {
	for _, v := range []string{"", "   ", "app amd64", "app\tamd64"} {
		err := checkArtifactParam(v)
		if err == nil {
			t.Fatalf("checkArtifactParam(%q) accepted", v)
		}
		for _, want := range []string{"1 to 200", "non-ASCII spaces", "only spaces"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message %q lacks %q", err.Error(), want)
			}
		}
	}
	if err := checkArtifactParam("web-frontend-stage-amd64"); err != nil {
		t.Errorf("plain name refused: %v", err)
	}
}
