package fieldauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"google.golang.org/api/idtoken"
)

// Bundle mirrors imdb-policy-service's compiled policy artifact. Additive
// changes only on the service side, so unknown JSON fields are ignored here.
type Bundle struct {
	Revision       int64                 `json:"revision"`
	GeneratedAt    time.Time             `json:"generatedAt"`
	DefaultPosture string                `json:"defaultPosture"`
	Fields         map[string]FieldEntry `json:"fields"`
	Principals     map[string][]string   `json:"principals"`
}

type FieldEntry struct {
	AllowedRoles []string `json:"allowedRoles"`
	Subgraph     string   `json:"subgraph"`
}

// poller keeps the latest bundle in memory, refreshing on an interval with
// ETag/If-None-Match so unchanged policy costs a 304. Fail-static: on any
// fetch error the last good bundle keeps serving; there is no TTL that
// expires it.
type poller struct {
	url      string
	interval time.Duration
	client   *http.Client
	logger   *zap.Logger
	// Google ID token (audience = policy service) identifying this router:
	// the policy service serves the principals map only to allowlisted
	// callers. nil without ADC (plain local runs) — the bundle still serves,
	// minus principals.
	tokens oauth2.TokenSource

	current atomic.Pointer[Bundle]
	etag    string
	cancel  context.CancelFunc
	done    chan struct{}
}

func newPoller(url string, interval time.Duration, logger *zap.Logger) *poller {
	tokens, err := idtoken.NewTokenSource(context.Background(), url)
	if err != nil {
		logger.Info("fieldAuth: no ID token source; bundle fetches are anonymous "+
			"(principals map may be withheld)", zap.Error(err))
		tokens = nil
	}
	return &poller{
		url:      url,
		interval: interval,
		client:   &http.Client{Timeout: 10 * time.Second},
		logger:   logger,
		tokens:   tokens,
		done:     make(chan struct{}),
	}
}

// start fetches once synchronously (so a healthy boot enforces from the very
// first request) and then refreshes in the background.
func (p *poller) start() {
	if err := p.fetchOnce(context.Background()); err != nil {
		p.logger.Error("fieldAuth: initial policy bundle fetch failed; "+
			"enforcing nothing until the first successful poll", zap.Error(err))
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go func() {
		defer close(p.done)
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := p.fetchOnce(ctx); err != nil && ctx.Err() == nil {
					p.logger.Warn("fieldAuth: bundle poll failed, serving last known good",
						zap.Error(err), zap.Int64("revision", p.revision()))
				}
			}
		}
	}()
}

func (p *poller) stop() {
	if p.cancel != nil {
		p.cancel()
		<-p.done
	}
}

func (p *poller) bundle() *Bundle {
	return p.current.Load()
}

func (p *poller) revision() int64 {
	if b := p.current.Load(); b != nil {
		return b.Revision
	}
	return -1
}

func (p *poller) fetchOnce(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url+"/v1/bundle", nil)
	if err != nil {
		return err
	}
	if p.etag != "" {
		req.Header.Set("If-None-Match", p.etag)
	}
	if p.tokens != nil {
		if tok, err := p.tokens.Token(); err == nil {
			req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		} else {
			p.logger.Warn("fieldAuth: ID token mint failed, fetching bundle anonymously", zap.Error(err))
		}
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		return nil
	case http.StatusOK:
		var bundle Bundle
		if err := json.NewDecoder(resp.Body).Decode(&bundle); err != nil {
			return fmt.Errorf("decoding bundle: %w", err)
		}
		previous := p.revision()
		p.current.Store(&bundle)
		p.etag = resp.Header.Get("ETag")
		if bundle.Revision != previous {
			p.logger.Info("fieldAuth: policy bundle updated",
				zap.Int64("revision", bundle.Revision),
				zap.Int("governed_fields", len(bundle.Fields)))
		}
		return nil
	default:
		return fmt.Errorf("policy service returned %s", resp.Status)
	}
}
