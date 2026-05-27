package api

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/raziel-ai/raziel/internal/auth"
	"github.com/raziel-ai/raziel/internal/config"
	"github.com/raziel-ai/raziel/internal/db"
	"github.com/raziel-ai/raziel/internal/provider"
	ptymanager "github.com/raziel-ai/raziel/internal/pty"
	"github.com/raziel-ai/raziel/internal/queue"
	"github.com/raziel-ai/raziel/internal/sandbox"
	"github.com/raziel-ai/raziel/internal/storage"
)

type Server struct {
	cfg             config.Config
	db              *db.DB
	store           storage.ArtifactStore
	queue           queue.Queue
	providers       *provider.Registry
	sandboxProvider sandbox.Provider
	wsTokens        *wsTokenStore
	ptyManager      *ptymanager.Manager
	log             *zap.Logger
	router          chi.Router
}

func New(cfg config.Config, database *db.DB, store storage.ArtifactStore, q queue.Queue, providers *provider.Registry, sbxProvider sandbox.Provider, log *zap.Logger) *Server {
	s := &Server{
		cfg:             cfg,
		db:              database,
		store:           store,
		queue:           q,
		providers:       providers,
		sandboxProvider: sbxProvider,
		wsTokens:        newWsTokenStore(),
		ptyManager:      ptymanager.NewManager(),
		log:             log,
	}
	s.router = s.buildRouter()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *Server) Addr() string {
	return fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
}

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(zapLogger(s.log))
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	// Public
	r.Get("/health", s.handleHealth)
	// Token-authenticated WebSocket endpoint (no Bearer header — token is in query param)
	r.Get("/v0/sandboxes/{sandboxID}/ws", s.handleSandboxWs)
	// Stats endpoint — auth required
	r.With(auth.SingleTenantMiddleware(s.cfg.APISecret)).Get("/v0/stats", s.handleStats)

	// Authenticated
	authMW := auth.SingleTenantMiddleware(s.cfg.APISecret)
	r.Group(func(r chi.Router) {
		r.Use(authMW)

		r.Get("/me", s.handleMe)

		r.Route("/v0/deployments", func(r chi.Router) {
			r.Get("/", s.handleListDeployments)
			r.Post("/", s.handleCreateDeployment)

			r.Route("/{deploymentID}", func(r chi.Router) {
				r.Get("/", s.handleGetDeployment)
				r.Put("/", s.handleRedeployDeployment)
				r.Delete("/", auth.RequireScope(auth.ScopeDelete)(http.HandlerFunc(s.handleDestroyDeployment)).ServeHTTP)
				r.Get("/logs", s.handleGetLogs)
				r.Post("/domains", s.handleAddDomain)
				r.Delete("/domains/{hostname}", s.handleRemoveDomain)
			})
		})

		r.Route("/v0/sandboxes", func(r chi.Router) {
			r.Get("/", s.handleListSandboxes)
			r.Post("/", s.handleCreateSandbox)

			r.Route("/{sandboxID}", func(r chi.Router) {
				r.Get("/", s.handleGetSandbox)
				r.Post("/stop", s.handleStopSandbox)
				r.Delete("/", s.handleDestroySandbox)
				r.Post("/ws-tokens", s.handleRegisterWsToken)
				r.Get("/tabs/{tabID}/scrollback", s.handleGetScrollback)
				r.Get("/diff", s.handleGetDiff)
			})
		})
	})

	return r
}
