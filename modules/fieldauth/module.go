// Package fieldauth enforces field-level governance at the router — the
// single runtime enforcement point of the federated graph. It polls the
// imdb-policy-service bundle (ETag, fail-static), resolves every operation's
// schema coordinates via a typed walk against the composed client schema,
// and rejects operations that select governed fields the caller's roles
// don't cover. The subgraphs declare (@governed), the policy service
// decides, this module enforces.
package fieldauth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/wundergraph/cosmo/router/core"
	"github.com/wundergraph/graphql-go-tools/v2/pkg/ast"
	"go.uber.org/zap"
)

const moduleID = "fieldAuth"

// RevisionHeader exposes which bundle revision decided each response, so
// policy propagation is observable from the outside (and in smoke tests).
const RevisionHeader = "X-Imdb-Policy-Revision"

type Module struct {
	// Populated from `modules.fieldAuth` in config.yaml via mapstructure.
	PolicyServiceURL    string `mapstructure:"policy_service_url"`
	PollIntervalSeconds int    `mapstructure:"poll_interval_seconds"`
	// "reject" (default) fails violating operations with PERMISSION_DENIED;
	// "log-only" logs the verdict and lets the operation through (rollout switch).
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
		m.Mode = "reject"
	}
	if m.Mode != "reject" && m.Mode != "log-only" {
		return fmt.Errorf("fieldAuth: mode must be reject or log-only, got %q", m.Mode)
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

	bundle := m.poller.bundle()
	if bundle == nil {
		// Never fetched a bundle yet: nothing is known to be governed.
		// Fail-open keeps the graph serving; the error was already logged.
		ctx.ResponseWriter().Header().Set(RevisionHeader, "unavailable")
		next.ServeHTTP(ctx.ResponseWriter(), ctx.Request())
		return
	}
	ctx.ResponseWriter().Header().Set(RevisionHeader, strconv.FormatInt(bundle.Revision, 10))

	coordinates, err := extractCoordinates(ctx.Operation().Content(), m.schema)
	if err != nil {
		// The router already validated the operation, so this indicates a
		// walker gap, not a bad request. Fail-open and log loudly.
		m.logger.Error("fieldAuth: coordinate extraction failed, allowing operation",
			zap.String("operation", ctx.Operation().Name()), zap.Error(err))
		next.ServeHTTP(ctx.ResponseWriter(), ctx.Request())
		return
	}

	var claims map[string]any
	if auth := ctx.Authentication(); auth != nil {
		claims = auth.Claims()
	}
	roles := resolveRoles(claims, bundle.Principals)
	denied := deniedCoordinates(coordinates, bundle, roles)
	if len(denied) == 0 {
		next.ServeHTTP(ctx.ResponseWriter(), ctx.Request())
		return
	}

	logFields := []zap.Field{
		zap.String("operation", ctx.Operation().Name()),
		zap.Strings("denied_fields", denied),
		zap.Strings("roles", roles),
		zap.Int64("bundle_revision", bundle.Revision),
	}
	if m.Mode == "log-only" {
		m.logger.Warn("fieldAuth: would deny (log-only mode)", logFields...)
		next.ServeHTTP(ctx.ResponseWriter(), ctx.Request())
		return
	}
	m.logger.Info("fieldAuth: operation denied", logFields...)
	writeDenied(ctx.ResponseWriter(), denied)
}

func writeDenied(w http.ResponseWriter, denied []string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]any{{
			"message": "not authorized to read: " + join(denied),
			"extensions": map[string]any{
				"code":         "PERMISSION_DENIED",
				"deniedFields": denied,
			},
		}},
	})
}

func join(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ", "
		}
		out += item
	}
	return out
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
