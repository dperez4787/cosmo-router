// Package fieldauth enforces field-level governance at the router — the
// single runtime enforcement point of the federated graph. It polls the
// imdb-policy-service bundle (ETag, fail-static), resolves every operation's
// schema coordinates via a typed walk against the composed client schema,
// and applies the policy: transparent redaction by default (denied fields
// stripped from the response, reported in extensions.governance), or reject
// / log-only via config. The subgraphs declare (@governed), the policy
// service decides, this module enforces.
package fieldauth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wundergraph/cosmo/router/core"
	"github.com/wundergraph/graphql-go-tools/v2/pkg/ast"
	"go.uber.org/zap"
)

const moduleID = "fieldAuth"

// RevisionHeader exposes which bundle revision decided each response, so
// policy propagation is observable from the outside (and in smoke tests).
const RevisionHeader = "X-Imdb-Policy-Revision"

// RolesHeader exposes the caller's resolved governance roles so clients can
// render a role badge without an extra endpoint.
const RolesHeader = "X-Imdb-Roles"

// The pinned router's CORS config has no expose_headers option, so this
// module sets the CORS expose header itself — required for browser JS on
// other origins (imdb-browser) to read the governance headers.
const exposeHeaders = "X-Imdb-Roles, X-Imdb-Policy-Revision, X-Imdb-Router"

type Module struct {
	// Populated from `modules.fieldAuth` in config.yaml via mapstructure.
	PolicyServiceURL    string `mapstructure:"policy_service_url"`
	PollIntervalSeconds int    `mapstructure:"poll_interval_seconds"`
	// "redact" (default): strip denied fields from the response and report
	// them in extensions.governance — no errors array, so naive clients keep
	// working. "reject" fails the whole operation with PERMISSION_DENIED.
	// "log-only" logs the verdict and lets everything through (rollout switch).
	Mode string `mapstructure:"mode"`
	// Where the baked execution config lives; its engineConfig.graphqlSchema
	// is the client schema coordinates resolve against.
	ExecutionConfigPath string `mapstructure:"execution_config_path"`

	logger *zap.Logger
	schema *ast.Document
	poller *poller
}

func (m *Module) Provision(ctx *core.ModuleContext) error {
	m.logger = ctx.Logger
	if m.PolicyServiceURL == "" {
		m.logger.Warn("fieldAuth disabled: no policy_service_url configured")
		return nil
	}
	if m.PollIntervalSeconds <= 0 {
		m.PollIntervalSeconds = 15
	}
	if m.Mode == "" {
		m.Mode = "redact"
	}
	if m.Mode != "redact" && m.Mode != "reject" && m.Mode != "log-only" {
		return fmt.Errorf("fieldAuth: mode must be redact, reject, or log-only, got %q", m.Mode)
	}
	if m.ExecutionConfigPath == "" {
		m.ExecutionConfigPath = "/config/execution-config.json"
	}

	schema, err := loadClientSchema(m.ExecutionConfigPath)
	if err != nil {
		return fmt.Errorf("fieldAuth: %w", err)
	}
	m.schema = schema

	m.poller = newPoller(m.PolicyServiceURL, time.Duration(m.PollIntervalSeconds)*time.Second, m.logger)
	m.poller.start()
	m.logger.Info("fieldAuth provisioned",
		zap.String("policy_service", m.PolicyServiceURL),
		zap.String("mode", m.Mode),
		zap.Int64("bundle_revision", m.poller.revision()))
	return nil
}

func (m *Module) Cleanup() error {
	if m.poller != nil {
		m.poller.stop()
	}
	return nil
}

func (m *Module) Middleware(ctx core.RequestContext, next http.Handler) {
	if m.poller == nil { // disabled
		next.ServeHTTP(ctx.ResponseWriter(), ctx.Request())
		return
	}

	headers := ctx.ResponseWriter().Header()
	headers.Set("Access-Control-Expose-Headers", exposeHeaders)

	bundle := m.poller.bundle()
	if bundle == nil {
		// Never fetched a bundle yet: nothing is known to be governed.
		// Fail-open keeps the graph serving; the error was already logged.
		headers.Set(RevisionHeader, "unavailable")
		next.ServeHTTP(ctx.ResponseWriter(), ctx.Request())
		return
	}
	headers.Set(RevisionHeader, strconv.FormatInt(bundle.Revision, 10))

	var claims map[string]any
	if auth := ctx.Authentication(); auth != nil {
		claims = auth.Claims()
	}
	roles := resolveRoles(claims, bundle.Principals)
	if len(roles) > 0 {
		headers.Set(RolesHeader, strings.Join(roles, ","))
	}

	selections, err := extractSelections(ctx.Operation().Content(), m.schema)
	if err != nil {
		// The router already validated the operation, so this indicates a
		// walker gap, not a bad request. Fail-open and log loudly.
		m.logger.Error("fieldAuth: selection extraction failed, allowing operation",
			zap.String("operation", ctx.Operation().Name()), zap.Error(err))
		next.ServeHTTP(ctx.ResponseWriter(), ctx.Request())
		return
	}

	denied := deniedCoordinates(uniqueCoordinates(selections), bundle, roles)
	if len(denied) == 0 {
		next.ServeHTTP(ctx.ResponseWriter(), ctx.Request())
		return
	}

	logFields := []zap.Field{
		zap.String("operation", ctx.Operation().Name()),
		zap.String("mode", m.Mode),
		zap.Strings("denied_fields", denied),
		zap.Strings("roles", roles),
		zap.Int64("bundle_revision", bundle.Revision),
	}
	switch {
	case m.Mode == "log-only":
		m.logger.Warn("fieldAuth: would deny (log-only mode)", logFields...)
		next.ServeHTTP(ctx.ResponseWriter(), ctx.Request())
	case m.Mode == "reject" || ctx.Operation().Type() == core.OperationTypeSubscription:
		// Streaming responses can't be buffered for redaction; subscriptions
		// selecting denied fields are rejected outright even in redact mode.
		m.logger.Info("fieldAuth: operation denied", logFields...)
		writeDenied(ctx.ResponseWriter(), denied)
	default:
		m.logger.Info("fieldAuth: redacting fields", logFields...)
		m.serveRedacted(ctx, next, selections, denied, roles, bundle.Revision)
	}
}

// serveRedacted buffers the downstream response and strips every response
// path belonging to a denied coordinate before forwarding. If the response
// isn't parseable JSON-with-data (unexpected for queries/mutations), it
// fails closed with a reject rather than leaking unredacted data.
func (m *Module) serveRedacted(ctx core.RequestContext, next http.Handler,
	selections []Selection, denied []string, roles []string, revision int64) {
	deniedSet := make(map[string]bool, len(denied))
	for _, coordinate := range denied {
		deniedSet[coordinate] = true
	}
	var paths [][]string
	for _, s := range selections {
		if deniedSet[s.Coordinate] {
			paths = append(paths, s.Path)
		}
	}

	real := ctx.ResponseWriter()
	recorder := &bufferingWriter{header: real.Header()}
	next.ServeHTTP(recorder, ctx.Request())

	if recorder.status != http.StatusOK {
		// Auth/validation errors carry no governed data; forward untouched.
		real.WriteHeader(recorder.status)
		_, _ = real.Write(recorder.buf.Bytes())
		return
	}
	body, ok := redactBody(recorder.buf.Bytes(), paths, denied, roles, revision)
	if !ok {
		m.logger.Error("fieldAuth: response not redactable, failing closed",
			zap.String("operation", ctx.Operation().Name()))
		writeDenied(real, denied)
		return
	}
	real.Header().Set("Content-Length", strconv.Itoa(len(body)))
	real.WriteHeader(http.StatusOK)
	_, _ = real.Write(body)
}

// bufferingWriter captures status and body while sharing the real response
// header map, so header writes from the engine still land.
type bufferingWriter struct {
	header http.Header
	buf    bytes.Buffer
	status int
}

func (b *bufferingWriter) Header() http.Header { return b.header }

func (b *bufferingWriter) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.buf.Write(p)
}

func (b *bufferingWriter) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}

func writeDenied(w http.ResponseWriter, denied []string) {
	w.Header().Del("Content-Length")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]any{{
			"message": "not authorized to read: " + strings.Join(denied, ", "),
			"extensions": map[string]any{
				"code":         "PERMISSION_DENIED",
				"deniedFields": denied,
			},
		}},
	})
}

func (m *Module) Module() core.ModuleInfo {
	return core.ModuleInfo{
		ID:       moduleID,
		Priority: 3, // after requestlog (1) and subgraphtoken (2)
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
	_ core.Provisioner             = (*Module)(nil)
	_ core.RouterMiddlewareHandler = (*Module)(nil)
	_ core.Cleaner                 = (*Module)(nil)
)
