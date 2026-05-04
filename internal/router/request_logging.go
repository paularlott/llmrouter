package router

import "net/http"

type statusLoggingResponseWriter struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func (w *statusLoggingResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.statusCode = statusCode
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *statusLoggingResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusLoggingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
