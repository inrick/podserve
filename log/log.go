package log // import "podserve/log"

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

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

type responseLog struct {
	RemoteAddr    string `json:"remote_addr"`
	XForwardedFor string `json:"x_forwarded_for"`
	Method        string `json:"method"`
	Path          string `json:"path"`
	Proto         string `json:"proto"`
	Status        int    `json:"status"`
	ContentLength string `json:"content_length"`
}

func extractResponseLog(w *ResponseWriter, r *http.Request) responseLog {
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
	return responseLog{
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
	t := time.Now().Format(time.RFC3339)
	msg := struct {
		logMessage
		responseLog
	}{
		logMessage{Level: "INFO", Time: t, Msg: "Sent response"},
		extractResponseLog(w, r),
	}
	bb, err := json.Marshal(&msg)
	if err != nil {
		panic(err)
	}
	log.Print(string(bb))
}

type logMessage struct {
	Level string `json:"level"`
	Time  string `json:"time"`
	Msg   string `json:"msg"`
}

func logf(level, format string, args ...interface{}) {
	t := time.Now().Format(time.RFC3339)
	msg := logMessage{
		Level: level,
		Time:  t,
		Msg:   fmt.Sprintf(format, args...),
	}
	bb, err := json.Marshal(&msg)
	if err != nil {
		panic(err)
	}
	log.Print(string(bb))
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
