package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type contextKey string

const requestIDKey contextKey = "request_id"

// requestIDFrom returns the correlation id for a request, or the empty string.
func requestIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// withRequestID assigns each request a correlation id, honouring an inbound
// X-Request-Id so that a caller's id survives across services, and echoes it
// back on the response.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = middleware.GetReqID(r.Context())
		}

		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// withLogging emits one structured line per request.
func withLogging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			recorder := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(recorder, r)

			level := slog.LevelInfo
			if recorder.Status() >= http.StatusInternalServerError {
				level = slog.LevelError
			}

			logger.LogAttrs(r.Context(), level, "http request",
				slog.String("request_id", requestIDFrom(r.Context())),
				slog.String("method", r.Method),
				// RoutePattern, not r.URL.Path: the pattern is
				// "/v1/accounts/{id}" rather than a distinct value per account,
				// which keeps log aggregation and metrics labels bounded.
				slog.String("route", routePattern(r)),
				slog.String("path", r.URL.Path),
				slog.Int("status", recorder.Status()),
				slog.Int("bytes", recorder.BytesWritten()),
				slog.Duration("duration", time.Since(started)),
			)
		})
	}
}

// routePattern returns the matched chi route pattern, falling back to a
// constant for unmatched requests.
func routePattern(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		if pattern := rctx.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	return "unmatched"
}
