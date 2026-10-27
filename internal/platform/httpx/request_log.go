package httpx

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// RequestLogger logs one structured line per request. It deliberately logs
// r.URL.Path only, never the query string: the WebSocket endpoint takes the
// JWT as ?token=..., and query strings may carry other secrets too. Mount it
// after RequestID so the request id is available.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			// WrapResponseWriter keeps http.Hijacker/Flusher working (WebSockets).
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				status := ww.Status()
				if status == 0 {
					status = http.StatusOK
				}
				logger.LogAttrs(r.Context(), slog.LevelInfo, "http request",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Int("status", status),
					slog.Duration("duration", time.Since(start)),
					slog.Int("bytes", ww.BytesWritten()),
					slog.String("request_id", RequestIDFromContext(r.Context())),
				)
			}()
			next.ServeHTTP(ww, r)
		})
	}
}
