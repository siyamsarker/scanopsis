// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClassifyReadTimeout(t *testing.T) {
	for name, err := range map[string]error{
		"deadline":         os.ErrDeadlineExceeded,
		"wrapped deadline": &net.OpError{Op: "read", Err: os.ErrDeadlineExceeded},
		"timeout error":    timeoutError{},
	} {
		for _, gz := range []bool{false, true} {
			rj := classifyRead(err, gz, 1<<20)
			if rj.status != http.StatusRequestTimeout || rj.code != "request_timeout" {
				t.Errorf("%s (gzip %v): %d %s", name, gz, rj.status, rj.code)
			}
		}
	}
	// A body that ends early is still a bad body, not a timeout.
	if rj := classifyRead(io.ErrUnexpectedEOF, true, 1<<20); rj.status != http.StatusBadRequest {
		t.Errorf("early end: %d", rj.status)
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// slowBody lets a test hand the body to the server in parts.
type slowBody struct {
	pr *io.PipeReader
	pw *io.PipeWriter
}

func newSlowBody() *slowBody {
	pr, pw := io.Pipe()
	return &slowBody{pr, pw}
}

// serve runs the handler in a real server whose own deadlines are short, so
// that only the route's deadlines let a slow upload through.
func serveIngest(t *testing.T, e *hEnv) *httptest.Server {
	t.Helper()
	ts := httptest.NewUnstartedServer(e.in.Handlers()["ingestTrivy"])
	ts.Config.ReadTimeout = 100 * time.Millisecond
	ts.Config.WriteTimeout = 100 * time.Millisecond
	ts.Start()
	t.Cleanup(ts.Close)
	return ts
}

// post sends body in two parts with a pause in between; with stall set it
// never sends the second part and waits for the answer.
func (e *hEnv) postSlow(url string, body []byte, gzipped bool, pause time.Duration, stall bool) (int, string) {
	e.t.Helper()
	sb := newSlowBody()
	half := len(body) / 2
	go func() {
		_, _ = sb.pw.Write(body[:half])
		if stall {
			return // the pipe stays open: the server waits for more
		}
		time.Sleep(pause)
		_, _ = sb.pw.Write(body[half:])
		_ = sb.pw.Close()
	}()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, sb.pr)
	if err != nil {
		e.t.Fatal(err)
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Authorization", "Bearer "+e.g.token(e.t, nil))
	req.Header.Set("Content-Type", "application/json")
	if gzipped {
		req.Header.Set("Content-Encoding", "gzip")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	_ = sb.pw.CloseWithError(errors.New("done"))
	return resp.StatusCode, string(b)
}

// The route's read deadline replaces the server's: a body that takes longer
// than the server's read timeout, and an answer later than its write timeout,
// still work.
func TestIngestSlowUploadIsAccepted(t *testing.T) {
	body := readFixture(t, "image-full.json")
	for _, gzipped := range []bool{false, true} {
		e := newHEnv(t, func(o *Options) { o.ReadTimeout = 3 * time.Second })
		ts := serveIngest(t, e)
		sent := body
		if gzipped {
			sent = gz(t, body)
		}
		code, resp := e.postSlow(ts.URL, sent, gzipped, 400*time.Millisecond, false)
		if code != http.StatusCreated {
			t.Errorf("gzip %v: %d %s", gzipped, code, resp)
		}
	}
}

// A body that stops coming is answered 408 request_timeout, whatever its
// encoding (it was 400 "not a complete, valid gzip stream" for gzip), the
// rejection is recorded, and the memory is given back.
func TestIngestStalledUploadIsAnswered408(t *testing.T) {
	body := readFixture(t, "image-full.json")
	for _, gzipped := range []bool{false, true} {
		e := newHEnv(t, func(o *Options) { o.ReadTimeout = 400 * time.Millisecond })
		ts := serveIngest(t, e)
		sent := body
		if gzipped {
			sent = gz(t, body)
		}
		started := time.Now()
		code, resp := e.postSlow(ts.URL, sent, gzipped, 0, true)
		if code != http.StatusRequestTimeout || !strings.Contains(resp, `"code":"request_timeout"`) {
			t.Errorf("gzip %v: %d %s", gzipped, code, resp)
		}
		if d := time.Since(started); d < 350*time.Millisecond || d > 5*time.Second {
			t.Errorf("gzip %v: answered after %v", gzipped, d)
		}
		if ev := e.audit.list(); len(ev) != 1 || ev[0].detail["reason"] != "request_timeout" || ev[0].detail["project_path"] != "platform/payments-api" {
			t.Errorf("gzip %v: audit = %+v", gzipped, ev)
		}
		if n := e.in.budget.inUse(); n != 0 {
			t.Errorf("gzip %v: %d bytes still held", gzipped, n)
		}
		if len(e.save.calls) != 0 {
			t.Errorf("gzip %v: a stalled upload was stored", gzipped)
		}
	}
}

func TestIngestTimeoutDefaults(t *testing.T) {
	e := newHEnv(t, nil)
	if e.in.opts.ReadTimeout != 5*time.Minute || e.in.opts.AdmitWait != 30*time.Second {
		t.Errorf("read timeout %v, admit wait %v", e.in.opts.ReadTimeout, e.in.opts.AdmitWait)
	}
}
