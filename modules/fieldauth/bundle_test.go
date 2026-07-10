package fieldauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestPollerEtagAndFailStatic(t *testing.T) {
	var revision atomic.Int64
	revision.Store(1)
	var failing atomic.Bool
	var hits, notModified atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		etag := fmt.Sprintf("%q", fmt.Sprint(revision.Load()))
		if r.Header.Get("If-None-Match") == etag {
			notModified.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		fmt.Fprintf(w, `{"revision":%d,"defaultPosture":"allow-unless-governed",
			"fields":{"Rating.numVotes":{"allowedRoles":["analyst"],"subgraph":"ratings"}},
			"principals":{}}`, revision.Load())
	}))
	defer server.Close()

	p := newPoller(server.URL, time.Hour, zap.NewNop())

	// Initial fetch stores the bundle.
	if err := p.fetchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.revision() != 1 {
		t.Fatalf("revision = %d", p.revision())
	}

	// Unchanged upstream: 304, same bundle retained.
	if err := p.fetchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if notModified.Load() != 1 || p.revision() != 1 {
		t.Fatalf("expected a 304 keeping revision 1; 304s=%d revision=%d",
			notModified.Load(), p.revision())
	}

	// Upstream failure: error reported, last good bundle keeps serving.
	failing.Store(true)
	if err := p.fetchOnce(context.Background()); err == nil {
		t.Fatal("expected error from failing upstream")
	}
	if p.bundle() == nil || p.revision() != 1 {
		t.Fatal("fail-static violated: lost last known good bundle")
	}

	// Recovery with a new revision replaces the bundle.
	failing.Store(false)
	revision.Store(2)
	if err := p.fetchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.revision() != 2 {
		t.Fatalf("revision = %d", p.revision())
	}
}
