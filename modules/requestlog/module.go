// Package requestlog is a deliberately small first module: it logs each
// GraphQL operation and stamps a response header. Its job is to prove the
// custom-module build/deploy pipeline end-to-end before the field-level
// authorization module is ported onto the same scaffolding.
package requestlog

import (
	"net/http"

	"github.com/wundergraph/cosmo/router/core"
	"go.uber.org/zap"
)

const moduleID = "requestLog"

// HeaderName is set on every router response so deploys of the custom build
// are observable from the outside (smoke tests assert on it).
const HeaderName = "X-Imdb-Router"

type Module struct {
	// Populated from `modules.requestLog` in config.yaml via mapstructure.
	HeaderValue string `mapstructure:"header_value"`

	logger *zap.Logger
}

func (m *Module) Provision(ctx *core.ModuleContext) error {
	if m.HeaderValue == "" {
		m.HeaderValue = "cosmo-custom"
	}
	m.logger = ctx.Logger
	m.logger.Info("requestLog module provisioned", zap.String("header_value", m.HeaderValue))
	return nil
}

func (m *Module) Middleware(ctx core.RequestContext, next http.Handler) {
	op := ctx.Operation()
	ctx.Logger().Info("graphql operation",
		zap.String("operation_name", op.Name()),
		zap.String("operation_type", op.Type()),
		zap.Uint64("operation_hash", op.Hash()),
	)
	ctx.ResponseWriter().Header().Set(HeaderName, m.HeaderValue)
	next.ServeHTTP(ctx.ResponseWriter(), ctx.Request())
}

func (m *Module) Module() core.ModuleInfo {
	return core.ModuleInfo{
		ID:       moduleID,
		Priority: 1,
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
)
