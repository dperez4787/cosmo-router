// Package subgraphtoken authenticates the router→subgraph hop with Cloud Run
// IAM instead of application-level auth: it attaches a Google-signed ID token
// (audience = the subgraph's origin) to every outgoing subgraph request. The
// subgraphs deploy with no allUsers invoker, so direct calls are rejected by
// Google's front end before Spring Boot ever sees them — no shared secrets,
// no subgraph code.
//
// On Cloud Run the tokens come from the metadata server as the runtime
// service account (cosmorouter-run), which holds roles/run.invoker on each
// subgraph. Locally, provide impersonated ADC:
//
//	gcloud auth application-default login \
//	  --impersonate-service-account cosmorouter-run@<project>.iam.gserviceaccount.com
package subgraphtoken

import (
	"context"
	"net/http"
	"sync"

	"github.com/wundergraph/cosmo/router/core"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"google.golang.org/api/idtoken"
)

const moduleID = "subgraphToken"

type Module struct {
	// Populated from `modules.subgraphToken` in config.yaml via mapstructure.
	Enabled bool `mapstructure:"enabled"`

	logger  *zap.Logger
	mu      sync.Mutex
	sources map[string]oauth2.TokenSource
}

func (m *Module) Provision(ctx *core.ModuleContext) error {
	m.logger = ctx.Logger
	m.sources = make(map[string]oauth2.TokenSource)
	if !m.Enabled {
		m.logger.Warn("subgraphToken disabled: subgraph requests carry no ID tokens")
	}
	return nil
}

// tokenSource lazily creates one self-refreshing source per audience. Sources
// are built on a background context because they outlive the request that
// first needed them; oauth2 caches the token until near expiry.
func (m *Module) tokenSource(audience string) (oauth2.TokenSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ts, ok := m.sources[audience]; ok {
		return ts, nil
	}
	ts, err := idtoken.NewTokenSource(context.Background(), audience)
	if err != nil {
		return nil, err
	}
	m.sources[audience] = ts
	return ts, nil
}

func (m *Module) OnOriginRequest(req *http.Request, _ core.RequestContext) (*http.Request, *http.Response) {
	if !m.Enabled {
		return req, nil
	}
	audience := req.URL.Scheme + "://" + req.URL.Host
	ts, err := m.tokenSource(audience)
	if err == nil {
		var tok *oauth2.Token
		if tok, err = ts.Token(); err == nil {
			// Replaces any client Authorization header: the client's JWT is
			// router-boundary auth and must not leak to the subgraphs.
			req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
			return req, nil
		}
	}
	// Forward unsigned rather than fail the operation: against locked-down
	// subgraphs Google's 403 surfaces in the GraphQL errors anyway, and
	// against public ones (local dev without ADC) the call still works.
	m.logger.Warn("subgraphToken: no ID token attached",
		zap.String("audience", audience), zap.Error(err))
	return req, nil
}

func (m *Module) Module() core.ModuleInfo {
	return core.ModuleInfo{
		ID:       moduleID,
		Priority: 2,
		New: func() core.Module {
			return &Module{}
		},
	}
}

func init() {
	core.RegisterModule(&Module{})
}

// Interface guards
var (
	_ core.Provisioner            = (*Module)(nil)
	_ core.EnginePreOriginHandler = (*Module)(nil)
)
