package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"os"
	"time"
)

var DebugLog bool

func init() {
	log.SetFlags(0)
	log.SetOutput(os.Stdout)
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
	return w.w.Write(buf)
}

func (w *ResponseWriter) WriteHeader(status int) {
	w.status = status
	w.w.WriteHeader(status)
}

type ResponseLog struct {
	RemoteAddr    string
	XForwardedFor string
	Method        string
	Path          string
	Proto         string
	Status        int
	ContentLength string
}

func extractResponseLog(w *ResponseWriter, r *http.Request) ResponseLog {
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
	return ResponseLog{
		RemoteAddr:    r.RemoteAddr,
		XForwardedFor: xForwardedFor,
		Method:        r.Method,
		Path:          uri,
		Proto:         r.Proto,
		Status:        w.status,
		ContentLength: contentLength,
	}
}

func LogResponse(w *ResponseWriter, r *http.Request) {
	rl := extractResponseLog(w, r)
	Info(
		"%s %s \"%s %s %s\" %d %s",
		rl.RemoteAddr,
		rl.XForwardedFor,
		rl.Method,
		rl.Path,
		rl.Proto,
		rl.Status,
		rl.ContentLength,
	)
	if DebugLog {
		buf, err := httputil.DumpRequest(r, false)
		if err != nil {
			return
		}
		Debug(string(buf))
	}
}

type LogMessage struct {
	Level string `json:"level"`
	Time  string `json:"time"`
	Msg   string `json:"msg"`
}

func logf(level, format string, args ...interface{}) {
	t := time.Now().Format("2006-01-02 15:04:05")
	msg := LogMessage{
		Level: level,
		Time:  t,
		Msg:   fmt.Sprintf(format, args...),
	}
	bb, err := json.Marshal(&msg)
	if err != nil {
		panic(err)
	}
	log.Printf(string(bb))
}

func Debug(format string, args ...interface{}) {
	logf("DEBUG", format, args...)
}

func Info(format string, args ...interface{}) {
	logf("INFO", format, args...)
}

func Warning(format string, args ...interface{}) {
	logf("WARNING", format, args...)
}

func Error(format string, args ...interface{}) {
	logf("ERROR", format, args...)
}
