package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"reflect"
	"time"
)

var DebugLog bool

func init() {
	log.SetFlags(0)
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

func extractResponseLog(w http.ResponseWriter, r *http.Request) ResponseLog {
	// This is unsafe but it's the easiest way to access the return status. Seems
	// silly that it's private anyway and unlikely to change.
	status := int(reflect.ValueOf(w).Elem().FieldByName("status").Int())
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
		Status:        status,
		ContentLength: contentLength,
	}
}

func LogResponse(w http.ResponseWriter, r *http.Request) {
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

func logf(level, format string, args ...interface{}) {
	t := time.Now().Format("[2006-01-02 15:04:05]")
	log.Printf(t+" ["+level+"] "+format, args...)
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
