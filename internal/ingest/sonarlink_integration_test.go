// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package ingest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeLinker records the calls of LinkWaiting and LinkDeclared.
type fakeLinker struct {
	mu       sync.Mutex
	calls    [][3]int64
	declared []declaredCall
	err      error
}

// declaredCall is a call of LinkDeclared.
type declaredCall struct {
	serviceID, gitlabProjectID int64
	key                        string
}

func (f *fakeLinker) LinkDeclared(_ context.Context, serviceID, gitlabProjectID int64, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.declared = append(f.declared, declaredCall{serviceID, gitlabProjectID, key})
	return f.err
}

func (f *fakeLinker) gotDeclared() []declaredCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.declared)
}

func (f *fakeLinker) LinkWaiting(_ context.Context, serviceID, gitlabProjectID, pipelineID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, [3]int64{serviceID, gitlabProjectID, pipelineID})
	return f.err
}

func (f *fakeLinker) got() [][3]int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// The upload that creates a service, or gives an administrator's service its
// GitLab project ID, links the SonarQube analyses of its own pipeline that
// waited for it, by the pipeline ID of its verified token. Later uploads do
// not.
func TestIntegrationFirstUploadLinksWaitingSonar(t *testing.T) {
	l := &fakeLinker{}
	e := newIEnv(t, func(o *Options) { o.SonarLinker = l })
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	res := e.mustUpload(upload{body: body})
	if want := [][3]int64{{res.ServiceID, 42, 1001}}; !slices.Equal(l.got(), want) {
		t.Fatalf("calls after the first upload = %v, want %v", l.got(), want)
	}
	e.mustUpload(upload{body: body, claims: func(c map[string]any) { c["job_id"] = "3000" }})
	if n := len(l.got()); n != 1 {
		t.Errorf("%d calls after a later upload", n)
	}

	// An administrator's service adopted by its first upload.
	var id int64
	if err := e.pool.QueryRow(context.Background(), `INSERT INTO services (name, gitlab_project_path, created_via)
		VALUES ('Orders', 'demo/orders-api', 'manual') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	e.mustUpload(upload{body: body, claims: func(c map[string]any) {
		c["project_id"], c["project_path"], c["pipeline_id"] = "7", "demo/orders-api", "2002"
	}})
	if got := l.got(); len(got) != 2 || got[1] != [3]int64{id, 7, 2002} {
		t.Errorf("calls after the adoption = %v", got)
	}
}

// A failure to link is logged and never fails the upload.
func TestIntegrationSonarLinkFailureKeepsUpload(t *testing.T) {
	l := &fakeLinker{err: errors.New("database gone")}
	e := newIEnv(t, func(o *Options) { o.SonarLinker = l })
	e.mustUpload(upload{body: []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)})
	if n := len(l.got()); n != 1 {
		t.Errorf("%d calls", n)
	}
	if !strings.Contains(e.logs.String(), "database gone") {
		t.Errorf("failure not logged:\n%s", e.logs.String())
	}
}
