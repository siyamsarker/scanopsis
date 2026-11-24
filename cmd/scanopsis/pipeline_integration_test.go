// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/siyamsarker/scanopsis/internal/config"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
	"github.com/siyamsarker/scanopsis/internal/testutil/fakegitlab"
	"github.com/siyamsarker/scanopsis/internal/testutil/fakesonar"
)

// bootAdmin starts the real server, completes the first-run setup and TOTP
// enrolment, and returns a client with a complete administrator session.
func bootAdmin(t *testing.T) (*client, string) {
	t.Helper()
	pool := storetest.New(t)
	addr := freeAddr(t)
	cfg, err := config.Load(env(map[string]string{
		"SCANOPSIS_DATABASE_URL": dbURL(t, pool),
		"SCANOPSIS_PUBLIC_URL":   "http://" + addr,
		"SCANOPSIS_LISTEN_ADDR":  addr,
		"SCANOPSIS_SECRET_KEY":   base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)),
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveApp(ctx, cfg, slog.New(slog.NewJSONHandler(logs, nil))) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serveApp returned %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("server did not stop")
		}
	})
	c := &client{t: t, base: "http://" + addr}
	deadline := time.Now().Add(20 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(t.Context(), "GET", c.base+"/readyz", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("server not ready; log:\n%s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if code, body := c.do("POST", "/api/v1/setup", map[string]string{"token": setupToken(t, logs.String()), "username": "root", "password": "a long enough password"}); code != 204 {
		t.Fatalf("setup = %d %v", code, body)
	}
	_, enr := c.do("POST", "/api/v1/auth/mfa/enrollment", nil)
	code, _ := totp.GenerateCode(enr["secret"].(string), time.Now())
	if status, body := c.do("POST", "/api/v1/auth/mfa/enrollment/confirm", map[string]string{"code": code}); status != 200 {
		t.Fatalf("confirm = %d %v", status, body)
	}
	return c, c.base
}

// raw sends a request with an explicit body and headers and decodes JSON.
func raw(t *testing.T, method, url string, body []byte, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func counts(m map[string]any) string {
	c, _ := m["counts"].(map[string]any)
	return fmt.Sprintf("%v/%v/%v/%v/%v", c["critical"], c["high"], c["medium"], c["low"], c["unknown"])
}

// TestIntegrationPipelineToDashboard follows real data end to end: a Trivy
// report uploaded with a GitLab CI ID token, and a SonarQube analysis
// delivered by a signed webhook, both read back through the API.
func TestIntegrationPipelineToDashboard(t *testing.T) {
	c, publicURL := bootAdmin(t)

	// A fake GitLab that signs CI ID tokens, and a fake SonarQube.
	gl := httptest.NewUnstartedServer(nil)
	gitlabURL := "http://" + gl.Listener.Addr().String()
	fake, err := fakegitlab.New(gitlabURL)
	if err != nil {
		t.Fatal(err)
	}
	gl.Config.Handler = fake.Handler()
	gl.Start()
	defer gl.Close()

	sq := fakesonar.New("squ_e2e_token")
	defer sq.Close()
	analysed := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	sq.Set("platform:payments-api", fakesonar.Project{
		Measures: map[string]string{
			"reliability_rating": "1.0", "security_rating": "2.0", "sqale_rating": "1.0",
			"security_review_rating": "3.0", "coverage": "81.3", "duplicated_lines_density": "2.4",
			"bugs": "0", "vulnerabilities": "1", "code_smells": "12",
		},
		GateStatus: "ERROR",
		Analyses:   []fakesonar.Analysis{{Key: "AX-1", Date: analysed, Revision: "0123456789abcdef0123456789abcdef01234567"}},
	})

	if code, body := c.do("PATCH", "/api/v1/settings", map[string]any{
		"gitlab.url": gitlabURL, "sonar.url": sq.URL, "sonar.token": "squ_e2e_token", "sonar.webhook_secret": "whsec-e2e",
	}); code != 200 {
		t.Fatalf("settings = %d %v", code, body)
	}

	// 1. Upload a real Trivy report (fixture produced by Trivy 0.76.2).
	report, err := os.ReadFile("../../internal/ingest/testdata/image-full.json")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	token, err := fake.SignJWT(map[string]any{
		"iss": gitlabURL, "aud": publicURL, "sub": "project_path:platform/payments-api:ref_type:branch:ref:main",
		"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nbf": now.Add(-time.Minute).Unix(),
		"project_id": "42", "project_path": "platform/payments-api", "namespace_path": "platform",
		"pipeline_id": "1001", "job_id": "5001", "ref": "main", "ref_type": "branch", "ref_protected": "true",
		"sha": "0123456789abcdef0123456789abcdef01234567",
	})
	if err != nil {
		t.Fatal(err)
	}
	code, res := raw(t, "POST", c.base+"/api/v1/ingest/trivy", report, map[string]string{
		"Authorization": "Bearer " + token, "Content-Type": "application/json",
	})
	// The numbers Trivy itself reports for this fixture (trivy convert).
	if code != 201 || counts(res) != "2/44/58/31/3" || res["fixable"] != 66.0 || res["service_created"] != true {
		t.Fatalf("upload = %d %v", code, res)
	}
	serviceID := int64(res["service_id"].(float64))

	// A token for another audience is refused.
	bad, _ := fake.SignJWT(map[string]any{"iss": gitlabURL, "aud": "https://elsewhere.example.com", "exp": now.Add(time.Hour).Unix(),
		"project_id": "42", "project_path": "platform/payments-api", "pipeline_id": "1", "job_id": "1", "ref": "main", "ref_protected": "true", "sha": "ab"})
	if code, body := raw(t, "POST", c.base+"/api/v1/ingest/trivy", report, map[string]string{"Authorization": "Bearer " + bad, "Content-Type": "application/json"}); code != 401 || errCode(body) != "invalid_token" {
		t.Fatalf("wrong audience = %d %v", code, body)
	}

	// 2. The dashboard API shows exactly those numbers.
	code, pf := c.do("GET", "/api/v1/portfolio", nil)
	items, _ := pf["items"].([]any)
	if code != 200 || len(items) != 1 || counts(items[0].(map[string]any)) != "2/44/58/31/3" {
		t.Fatalf("portfolio = %d %v", code, pf)
	}
	code, ov := c.do("GET", fmt.Sprintf("/api/v1/services/%d/overview", serviceID), nil)
	if code != 200 || ov["ref"] != "main" || counts(ov) != "2/44/58/31/3" || ov["fixable"] != 66.0 {
		t.Fatalf("overview = %d %v", code, ov)
	}
	code, vulns := c.do("GET", fmt.Sprintf("/api/v1/services/%d/vulnerabilities?severity=CRITICAL", serviceID), nil)
	if list, _ := vulns["items"].([]any); code != 200 || len(list) != 2 {
		t.Fatalf("critical vulnerabilities = %d %v", code, vulns)
	}
	code, scans := c.do("GET", fmt.Sprintf("/api/v1/services/%d/scans", serviceID), nil)
	list, _ := scans["items"].([]any)
	if code != 200 || len(list) != 1 {
		t.Fatalf("scans = %d %v", code, scans)
	}
	scanID := int64(list[0].(map[string]any)["id"].(float64))
	code, scan := c.do("GET", fmt.Sprintf("/api/v1/scans/%d", scanID), nil)
	if code != 200 || scan["job_url"] != gitlabURL+"/platform/payments-api/-/jobs/5001" || scan["gitlab_pipeline_id"] != 1001.0 {
		t.Fatalf("scan = %d %v", code, scan)
	}

	// 3. SonarQube: link the project and deliver a signed webhook.
	if code, body := c.do("PATCH", fmt.Sprintf("/api/v1/services/%d", serviceID), map[string]any{"sonar_project_key": "platform:payments-api"}); code != 200 {
		t.Fatalf("link = %d %v", code, body)
	}
	hook, _ := json.Marshal(map[string]any{
		"serverUrl": sq.URL, "taskId": "task-e2e", "status": "SUCCESS",
		"analysedAt": analysed.Format("2006-01-02T15:04:05-0700"), "revision": "0123456789abcdef0123456789abcdef01234567",
		"project":     map[string]any{"key": "platform:payments-api", "name": "Payments API"},
		"qualityGate": map[string]any{"name": "Sonar way", "status": "ERROR", "conditions": []any{}},
		"properties":  map[string]string{"sonar.analysis.pipelineId": "1001"},
	})
	mac := hmac.New(sha256.New, []byte("whsec-e2e"))
	mac.Write(hook)
	sig := hex.EncodeToString(mac.Sum(nil))
	if code, body := raw(t, "POST", c.base+"/api/v1/hooks/sonarqube", hook, map[string]string{"X-Sonar-Webhook-HMAC-SHA256": "00" + sig[2:], "Content-Type": "application/json"}); code != 401 {
		t.Fatalf("bad signature = %d %v", code, body)
	}
	if code, body := raw(t, "POST", c.base+"/api/v1/hooks/sonarqube", hook, map[string]string{"X-Sonar-Webhook-HMAC-SHA256": sig, "Content-Type": "application/json"}); code != 202 {
		t.Fatalf("webhook = %d %v", code, body)
	}
	var latest map[string]any
	deadline := time.Now().Add(10 * time.Second)
	for latest == nil && time.Now().Before(deadline) {
		_, sonar := c.do("GET", fmt.Sprintf("/api/v1/services/%d/sonar", serviceID), nil)
		latest, _ = sonar["latest"].(map[string]any)
		if latest == nil {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if latest == nil || latest["quality_gate_status"] != "ERROR" || latest["reliability_rating"] != "A" ||
		latest["security_rating"] != "B" || latest["maintainability_rating"] != "A" || latest["coverage"] != 81.3 ||
		latest["mode"] != "standard" || latest["gitlab_pipeline_id"] != 1001.0 {
		t.Fatalf("sonar latest = %v", latest)
	}

	// 4. The portfolio now joins both sources; integration health agrees.
	_, pf = c.do("GET", "/api/v1/portfolio", nil)
	item := pf["items"].([]any)[0].(map[string]any)
	if item["quality_gate_status"] != "ERROR" || item["security_rating"] != "B" {
		t.Fatalf("portfolio with sonar = %v", item)
	}
	if totals := pf["totals"].(map[string]any); totals["failing_quality_gates"] != 1.0 || totals["services_with_critical"] != 1.0 {
		t.Fatalf("totals = %v", totals)
	}
	if code, h := c.do("GET", "/api/v1/admin/integrations/trivy", nil); code != 200 || h["configured"] != true || h["last_upload_at"] == nil || h["rejections_24h"] != 1.0 {
		t.Fatalf("trivy health = %d %v", code, h)
	}
	// The audit log shows the rejected upload (wrong audience).
	code, al := c.do("GET", "/api/v1/admin/audit?action=ingest.rejected", nil)
	if events, _ := al["items"].([]any); code != 200 || len(events) != 1 || events[0].(map[string]any)["actor"] != "ci:unknown" {
		t.Fatalf("audit log = %d %v", code, al)
	}
	code, sh := c.do("GET", "/api/v1/admin/integrations/sonarqube", nil)
	deliveries, _ := sh["recent_deliveries"].([]any)
	if code != 200 || sh["configured"] != true || len(deliveries) != 1 || deliveries[0].(map[string]any)["outcome"] != "linked" {
		t.Fatalf("sonar health = %d %v", code, sh)
	}
}

// TestIntegrationSonarBeforeFirstScan follows a pipeline that runs its
// SonarQube analysis before its first Trivy scan: the analysis arrives while
// no service has the project, waits, and is shown as soon as the first upload
// creates the service, without an administrator's step.
func TestIntegrationSonarBeforeFirstScan(t *testing.T) {
	c, publicURL := bootAdmin(t)

	gl := httptest.NewUnstartedServer(nil)
	gitlabURL := "http://" + gl.Listener.Addr().String()
	fake, err := fakegitlab.New(gitlabURL)
	if err != nil {
		t.Fatal(err)
	}
	gl.Config.Handler = fake.Handler()
	gl.Start()
	defer gl.Close()

	sq := fakesonar.New("squ_e2e_token")
	defer sq.Close()
	analysed := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	sq.Set("demo:orders-api", fakesonar.Project{
		Measures:   fakesonar.StandardMeasures(),
		GateStatus: "OK",
		Analyses:   []fakesonar.Analysis{{Key: "AX-7", Date: analysed, Revision: "0123456789abcdef0123456789abcdef01234567"}},
	})
	if code, body := c.do("PATCH", "/api/v1/settings", map[string]any{
		"gitlab.url": gitlabURL, "sonar.url": sq.URL, "sonar.token": "squ_e2e_token", "sonar.webhook_secret": "whsec-e2e",
	}); code != 200 {
		t.Fatalf("settings = %d %v", code, body)
	}

	// 1. The analysis arrives first and waits.
	hook, _ := json.Marshal(map[string]any{
		"serverUrl": sq.URL, "taskId": "task-early", "status": "SUCCESS",
		"analysedAt": analysed.Format("2006-01-02T15:04:05-0700"),
		"project":    map[string]any{"key": "demo:orders-api", "name": "Orders API"},
		"properties": map[string]string{"sonar.analysis.gitlabProjectId": "7", "sonar.analysis.pipelineId": "1001"},
	})
	mac := hmac.New(sha256.New, []byte("whsec-e2e"))
	mac.Write(hook)
	if code, body := raw(t, "POST", c.base+"/api/v1/hooks/sonarqube", hook, map[string]string{
		"X-Sonar-Webhook-HMAC-SHA256": hex.EncodeToString(mac.Sum(nil)), "Content-Type": "application/json",
	}); code != 202 {
		t.Fatalf("webhook = %d %v", code, body)
	}
	delivery := func() map[string]any {
		_, sh := c.do("GET", "/api/v1/admin/integrations/sonarqube", nil)
		list, _ := sh["recent_deliveries"].([]any)
		if len(list) != 1 {
			return nil
		}
		return list[0].(map[string]any)
	}
	deadline := time.Now().Add(10 * time.Second)
	for d := delivery(); d == nil || d["outcome"] == "pending"; d = delivery() {
		if time.Now().After(deadline) {
			t.Fatalf("delivery not processed: %v", d)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if d := delivery(); d["outcome"] != "unlinked" {
		t.Fatalf("before the first scan: %v", d)
	}

	// 2. The first Trivy upload creates the service, which is linked at once.
	now := time.Now()
	token, err := fake.SignJWT(map[string]any{
		"iss": gitlabURL, "aud": publicURL, "sub": "project_path:demo/orders-api:ref_type:branch:ref:main",
		"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nbf": now.Add(-time.Minute).Unix(),
		"project_id": "7", "project_path": "demo/orders-api", "namespace_path": "demo",
		"pipeline_id": "1001", "job_id": "5001", "ref": "main", "ref_type": "branch", "ref_protected": "true",
		"sha": "0123456789abcdef0123456789abcdef01234567",
	})
	if err != nil {
		t.Fatal(err)
	}
	code, res := raw(t, "POST", c.base+"/api/v1/ingest/trivy", []byte(`{"SchemaVersion":2,"ArtifactName":"orders-api:1"}`), map[string]string{
		"Authorization": "Bearer " + token, "Content-Type": "application/json",
	})
	if code != 201 || res["service_created"] != true {
		t.Fatalf("upload = %d %v", code, res)
	}
	serviceID := int64(res["service_id"].(float64))
	if d := delivery(); d["outcome"] != "linked" || d["service_name"] != "orders-api" {
		t.Errorf("after the first scan: %v", d)
	}
	_, svc := c.do("GET", fmt.Sprintf("/api/v1/services/%d", serviceID), nil)
	if svc["sonar_project_key"] != "demo:orders-api" {
		t.Errorf("service = %v", svc)
	}
	var latest map[string]any
	for deadline = time.Now().Add(10 * time.Second); latest == nil && time.Now().Before(deadline); {
		_, sonar := c.do("GET", fmt.Sprintf("/api/v1/services/%d/sonar", serviceID), nil)
		if latest, _ = sonar["latest"].(map[string]any); latest == nil {
			time.Sleep(100 * time.Millisecond)
		}
	}
	// The card says in which pipeline the analysis ran.
	if latest == nil || latest["quality_gate_status"] != "OK" || latest["gitlab_pipeline_id"] != float64(1001) {
		t.Fatalf("sonar latest = %v", latest)
	}
}

// TestIntegrationSonarKeyDeclared follows a pipeline whose upload declares
// its SonarQube key: an analysis of another project that names the same
// GitLab project and pipeline is never linked, the declared project's
// analysis that waited is, and the card says in which pipeline it ran.
func TestIntegrationSonarKeyDeclared(t *testing.T) {
	c, publicURL := bootAdmin(t)

	gl := httptest.NewUnstartedServer(nil)
	gitlabURL := "http://" + gl.Listener.Addr().String()
	fake, err := fakegitlab.New(gitlabURL)
	if err != nil {
		t.Fatal(err)
	}
	gl.Config.Handler = fake.Handler()
	gl.Start()
	defer gl.Close()

	sq := fakesonar.New("squ_e2e_token")
	defer sq.Close()
	analysed := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	for _, key := range []string{"demo:orders-api", "other:planted"} {
		sq.Set(key, fakesonar.Project{
			Measures: fakesonar.StandardMeasures(), GateStatus: "OK",
			Analyses: []fakesonar.Analysis{{Key: "AX-" + key, Date: analysed}},
		})
	}
	if code, body := c.do("PATCH", "/api/v1/settings", map[string]any{
		"gitlab.url": gitlabURL, "sonar.url": sq.URL, "sonar.token": "squ_e2e_token", "sonar.webhook_secret": "whsec-e2e",
	}); code != 200 {
		t.Fatalf("settings = %d %v", code, body)
	}

	// 1. The planted analysis arrives first, then the real one; both name
	// GitLab project 7 and pipeline 1001, and both wait.
	for _, d := range []struct{ task, key string }{{"task-planted", "other:planted"}, {"task-real", "demo:orders-api"}} {
		hook, _ := json.Marshal(map[string]any{
			"serverUrl": sq.URL, "taskId": d.task, "status": "SUCCESS",
			"analysedAt": analysed.Format("2006-01-02T15:04:05-0700"),
			"project":    map[string]any{"key": d.key, "name": d.key},
			"properties": map[string]string{"sonar.analysis.gitlabProjectId": "7", "sonar.analysis.pipelineId": "1001"},
		})
		mac := hmac.New(sha256.New, []byte("whsec-e2e"))
		mac.Write(hook)
		if code, body := raw(t, "POST", c.base+"/api/v1/hooks/sonarqube", hook, map[string]string{
			"X-Sonar-Webhook-HMAC-SHA256": hex.EncodeToString(mac.Sum(nil)), "Content-Type": "application/json",
		}); code != 202 {
			t.Fatalf("webhook = %d %v", code, body)
		}
	}
	deliveries := func() map[string]map[string]any {
		_, sh := c.do("GET", "/api/v1/admin/integrations/sonarqube", nil)
		list, _ := sh["recent_deliveries"].([]any)
		out := map[string]map[string]any{}
		for _, d := range list {
			m := d.(map[string]any)
			out[m["task_id"].(string)] = m
		}
		return out
	}
	deadline := time.Now().Add(10 * time.Second)
	for ds := deliveries(); len(ds) != 2 || ds["task-planted"]["outcome"] == "pending" || ds["task-real"]["outcome"] == "pending"; ds = deliveries() {
		if time.Now().After(deadline) {
			t.Fatalf("deliveries not processed: %v", ds)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 2. The first upload of the pipeline declares the key.
	now := time.Now()
	token, err := fake.SignJWT(map[string]any{
		"iss": gitlabURL, "aud": publicURL, "sub": "project_path:demo/orders-api:ref_type:branch:ref:main",
		"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nbf": now.Add(-time.Minute).Unix(),
		"project_id": "7", "project_path": "demo/orders-api", "namespace_path": "demo",
		"pipeline_id": "1001", "job_id": "5001", "ref": "main", "ref_type": "branch", "ref_protected": "true",
		"sha": "0123456789abcdef0123456789abcdef01234567",
	})
	if err != nil {
		t.Fatal(err)
	}
	code, res := raw(t, "POST", c.base+"/api/v1/ingest/trivy?sonar_key=demo:orders-api", []byte(`{"SchemaVersion":2,"ArtifactName":"orders-api:1"}`), map[string]string{
		"Authorization": "Bearer " + token, "Content-Type": "application/json",
	})
	if code != 201 || res["service_created"] != true || res["sonar_key_ignored"] != nil {
		t.Fatalf("upload = %d %v", code, res)
	}
	serviceID := int64(res["service_id"].(float64))
	_, svc := c.do("GET", fmt.Sprintf("/api/v1/services/%d", serviceID), nil)
	if svc["sonar_project_key"] != "demo:orders-api" {
		t.Errorf("service = %v", svc)
	}
	ds := deliveries()
	if ds["task-real"]["outcome"] != "linked" || ds["task-planted"]["outcome"] != "unlinked" {
		t.Errorf("deliveries after the upload: %v", ds)
	}
	var latest map[string]any
	for deadline = time.Now().Add(10 * time.Second); latest == nil && time.Now().Before(deadline); {
		_, sonar := c.do("GET", fmt.Sprintf("/api/v1/services/%d/sonar", serviceID), nil)
		if latest, _ = sonar["latest"].(map[string]any); latest == nil {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if latest == nil || latest["gitlab_pipeline_id"] != float64(1001) {
		t.Fatalf("sonar latest = %v", latest)
	}

	// 3. A second project's upload cannot take the key: it is stored, and
	// the answer says why the key was ignored.
	token2, err := fake.SignJWT(map[string]any{
		"iss": gitlabURL, "aud": publicURL, "sub": "project_path:demo/copycat:ref_type:branch:ref:main",
		"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nbf": now.Add(-time.Minute).Unix(),
		"project_id": "8", "project_path": "demo/copycat", "namespace_path": "demo",
		"pipeline_id": "1002", "job_id": "5002", "ref": "main", "ref_type": "branch", "ref_protected": "true",
		"sha": "0123456789abcdef0123456789abcdef01234567",
	})
	if err != nil {
		t.Fatal(err)
	}
	code, res = raw(t, "POST", c.base+"/api/v1/ingest/trivy?sonar_key=demo:orders-api", []byte(`{"SchemaVersion":2,"ArtifactName":"copycat:1"}`), map[string]string{
		"Authorization": "Bearer " + token2, "Content-Type": "application/json",
	})
	if code != 201 || res["sonar_key_ignored"] != "key_linked_to_other_service" {
		t.Fatalf("copycat upload = %d %v", code, res)
	}
}
