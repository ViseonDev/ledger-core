package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/ViseonDev/ledger-core/internal/app"
	"github.com/ViseonDev/ledger-core/internal/platform/observability"
)

// maxRequestBody bounds how much a client can send.
const maxRequestBody = 256 << 10

// errBadRequest marks malformed input so problemFor can map it to 400.
var errBadRequest = errors.New("malformed request")

// Server holds the dependencies the handlers need.
type Server struct {
	ledger  *app.Ledger
	logger  *slog.Logger
	metrics *observability.Metrics
	ready   func() error
	timeout time.Duration
}

// Config configures the HTTP server.
type Config struct {
	Ledger  *app.Ledger
	Logger  *slog.Logger
	Metrics *observability.Metrics
	// Ready reports whether dependencies are reachable. Returning an error
	// makes /readyz fail.
	Ready          func() error
	RequestTimeout time.Duration
}

// NewServer builds the HTTP handler.
func NewServer(cfg Config) *Server {
	return &Server{
		ledger:  cfg.Ledger,
		logger:  cfg.Logger,
		metrics: cfg.Metrics,
		ready:   cfg.Ready,
		timeout: cfg.RequestTimeout,
	}
}

// Routes returns the fully-wired router.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(withRequestID)
	r.Use(middleware.RealIP) //nolint:staticcheck // RealIP middleware as required by project spec
	r.Use(middleware.Recoverer)
	r.Use(withLogging(s.logger))
	r.Use(s.withMetrics)
	r.Use(middleware.Timeout(s.timeout))

	// Probes live outside /v1.
	r.Get("/healthz", s.handleHealth)
	r.Get("/readyz", s.handleReady)
	r.Method(http.MethodGet, "/metrics", s.metrics.Handler())

	r.Route("/v1", func(r chi.Router) {
		r.Route("/accounts", func(r chi.Router) {
			r.Post("/", s.handleOpenAccount)
			r.Get("/{code}", s.handleGetAccount)
			r.Get("/{id}/balance", s.handleGetBalance)
		})
		r.Route("/transactions", func(r chi.Router) {
			r.Post("/", s.handlePostTransaction)
			r.Get("/{id}", s.handleGetTransaction)
			r.Post("/{id}/reversal", s.handleReverseTransaction)
		})
	})

	return r
}

func (s *Server) withMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(recorder, r)

		s.metrics.ObserveHTTP(r.Method, routePattern(r), recorder.Status(), time.Since(started))
	})
}

// handleHealth is liveness only, it does not check the database.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"status":"ok"}`) //nolint:errcheck
}

// handleReady checks the database.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.ready(); err != nil {
		s.logger.WarnContext(r.Context(), "readiness check failed", slog.String("error", err.Error()))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"status":"unavailable"}`) //nolint:errcheck
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"status":"ready"}`) //nolint:errcheck
}
