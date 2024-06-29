package main

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
)

func responseLoggerFunc(f func(w http.ResponseWriter, r *http.Request)) http.Handler {
	return responseLogger(http.HandlerFunc(f))
}

func responseLogger(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w = NewResponseWriter(w)
		defer LogResponse(w.(*ResponseWriter), r)
		h.ServeHTTP(w, r)
	})
}

type ResponseWriter struct {
	w      http.ResponseWriter
	status int
}

func NewResponseWriter(w http.ResponseWriter) *ResponseWriter {
	return &ResponseWriter{w: w, status: 200}
}

func (w *ResponseWriter) Header() http.Header {
	return w.w.Header()
}

func (w *ResponseWriter) Write(buf []byte) (int, error) {
	if w.status/100 != 2 {
		// If status is not 2xx, skip writing the body. This is because this
		// ResponseWriter is sent to http.ServeContent that writes an error message
		// to the wire in case something fails. We'd rather just log it and send
		// only the status to the client.
		err := errors.New(string(buf))
		slog.Error("http response error", "error", err, "status", w.status, "tag", TagHttp)
		return len(buf), nil
	}
	return w.w.Write(buf)
}

func (w *ResponseWriter) WriteHeader(status int) {
	w.status = status
	w.w.WriteHeader(status)
}

func LogResponse(w *ResponseWriter, r *http.Request) {
	uri := r.RequestURI
	if uri == "" {
		uri = r.URL.RequestURI()
	}
	args := []any{
		"tag", TagHttp,
		"remote_addr", r.RemoteAddr,
		"method", r.Method,
		"path", uri,
		"proto", r.Proto,
		"status", w.status,
	}
	if contentLength := w.Header().Get("Content-Length"); contentLength != "" {
		if length, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			args = append(args, "content_length", length)
		}
	}
	if xForwardedFor := r.Header.Get("X-Forwarded-For"); xForwardedFor != "" {
		args = append(args, "x_forwarded_for", xForwardedFor)
	}
	if userAgent := r.UserAgent(); userAgent != "" {
		args = append(args, "user_agent", userAgent)
	}
	slog.Info("Sent response", args...)
}
