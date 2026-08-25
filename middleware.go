package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

const (
	LogContextKey contextKey = "log_context"
	RequestIDKey  contextKey = "request_id"
)

type LogContext struct {
	Username  string
	RequestID string
	Error     error
}

type spyReadCloser struct {
	io.ReadCloser
	bytesRead int
}

type spyResponseWriter struct {
	http.ResponseWriter
	bytesWritten int
	statusCode   int
}

func (r *spyReadCloser) Read(p []byte) (int, error) {
	if r.ReadCloser == nil {
		return 0, io.EOF
	}
	n, err := r.ReadCloser.Read(p)
	r.bytesRead += n
	return n, err
}

func (w *spyResponseWriter) Write(p []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}

	n, err := w.ResponseWriter.Write(p)
	w.bytesWritten += n
	return n, err
}

func (w *spyResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func httpError(ctx context.Context, w http.ResponseWriter, err error, status int) {
	if logCtx, ok := ctx.Value(LogContextKey).(*LogContext); ok {
		logCtx.Error = err
	}
	http.Error(w, err.Error(), status)
}

func generateRequestID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405.00000000")))
	}
	return hex.EncodeToString(buf)
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}

		w.Header().Set("X-Request-ID", requestID)

		if logCtx, ok := r.Context().Value(LogContextKey).(*LogContext); ok {
			logCtx.RequestID = requestID
		}

		ctx := context.WithValue(r.Context(), RequestIDKey, requestID)
		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			logctx := &LogContext{}

			ctx := context.WithValue(r.Context(), LogContextKey, logctx)
			r = r.WithContext(ctx)

			spyReader := &spyReadCloser{ReadCloser: r.Body}
			spyWriter := &spyResponseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			r.Body = spyReader
			next.ServeHTTP(spyWriter, r)

			clientIP := r.RemoteAddr
			if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
				clientIP = host
			}

			args := []any{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("client_ip", clientIP),
				slog.Duration("duration", time.Since(start)),
				slog.Int("request_body_bytes", spyReader.bytesRead),
				slog.Int("response_status", spyWriter.statusCode),
				slog.Int("response_body_bytes", spyWriter.bytesWritten),
			}
			if logctx.Username != "" {
				args = append(args, slog.String("user", logctx.Username))
			}
			if logctx.Error != nil {
				args = append(args, slog.String("error", logctx.Error.Error()))
			}
			if logctx.RequestID != "" {
				args = append(args, slog.String("request_id", logctx.RequestID))
			}

			logger.Info("Served request", args...)
		})
	}
}
