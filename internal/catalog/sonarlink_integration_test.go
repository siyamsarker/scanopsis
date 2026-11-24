// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package catalog

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"
)

// fakeLinker records the calls of LinkWaiting.
type fakeLinker struct {
	mu    sync.Mutex
	calls [][2]int64
	err   error
}

func (f *fakeLinker) LinkWaiting(_ context.Context, serviceID, gitlabProjectID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, [2]int64{serviceID, gitlabProjectID})
	return f.err
}

func (f *fakeLinker) got() [][2]int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Archived services are never linked, so SonarQube analyses of an archived
// service's project wait. Unarchiving a service that has a GitLab project ID
// and no SonarQube key links them; any other change does not.
func TestUnarchiveLinksWaitingSonar(t *testing.T) {
	c, pool, _ := newCatalog(t, "")
	l := &fakeLinker{}
	c.SetSonarLinker(l)
	free := addService(t, pool, "orders-api", "demo/orders-api", true)
	keyed := addService(t, pool, "billing", "demo/billing", true)
	noID := addService(t, pool, "docs", "demo/docs", true)
	mustExec(t, pool, `UPDATE services SET gitlab_project_id = 7 WHERE id = $1`, free)
	mustExec(t, pool, `UPDATE services SET gitlab_project_id = 8, sonar_project_key = 'demo:billing' WHERE id = $1`, keyed)

	patch := func(id int64, body string) {
		t.Helper()
		if rec := call(t, c, "updateService", "PATCH", "/", body, strconv.FormatInt(id, 10)); rec.Code != http.StatusOK {
			t.Fatalf("patch %d %s = %d %s", id, body, rec.Code, rec.Body.String())
		}
	}
	patch(free, `{"name":"Orders"}`)
	if n := len(l.got()); n != 0 {
		t.Fatalf("%d calls for a rename", n)
	}
	patch(free, `{"archived":false}`)
	patch(keyed, `{"archived":false}`)
	patch(noID, `{"archived":false}`)
	if want := [][2]int64{{free, 7}}; !slices.Equal(l.got(), want) {
		t.Errorf("calls = %v, want %v", l.got(), want)
	}

	// A failure is logged; the update stands.
	l.err = errors.New("database gone")
	patch(free, `{"archived":true}`)
	patch(free, `{"archived":false}`)
	if n := len(l.got()); n != 2 {
		t.Errorf("%d calls", n)
	}
}
