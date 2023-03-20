package main

import (
	"errors"
	"net/http"

	"golang.org/x/exp/slog"
)

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
		slog.Error("http response error", err, "status", w.status, "tag", TagHttp)
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
	contentLength := w.Header().Get("Content-Length")
	if contentLength == "" {
		contentLength = "-"
	}
	xForwardedFor := r.Header.Get("X-Forwarded-For")
	if xForwardedFor == "" {
		xForwardedFor = "-"
	}
	slog.Info(
		"Sent response",
		"tag", TagHttp,
		"remote_addr", r.RemoteAddr,
		"x_forwarded_for", xForwardedFor,
		"method", r.Method,
		"path", uri,
		"proto", r.Proto,
		"status", w.status,
		"content_length", contentLength,
	)
}
