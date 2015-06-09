package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"gopkg.in/tylerb/graceful.v1"
	"gopkg.in/yaml.v2"

	log "github.com/Sirupsen/logrus"
	"github.com/gorilla/mux"
	"github.com/rs/cors"
)

func NewRewriteReverseProxy(basePath string, redirectUrl string) *httputil.ReverseProxy {
	target, err := url.Parse(redirectUrl)
	if err != nil {
		log.Fatal(err)
	}
	targetQuery := target.RawQuery
	director := func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = strings.TrimPrefix(req.URL.Path, basePath)
		if targetQuery == "" || req.URL.RawQuery == "" {
			req.URL.RawQuery = targetQuery + req.URL.RawQuery
		} else {
			req.URL.RawQuery = targetQuery + "&" + req.URL.RawQuery
		}
	}
	return &httputil.ReverseProxy{Director: director}
}

type Config struct {
	Routes map[string]string
}

type StatusLoggingResponseWriter struct {
	status int
	http.ResponseWriter
}

func NewStatusLoggingResponseWriter(res http.ResponseWriter) *StatusLoggingResponseWriter {
	// Default the status code to 200.
	return &StatusLoggingResponseWriter{200, res}
}

func (w *StatusLoggingResponseWriter) Status() int {
	return w.status
}

// Satisfy the http.ResponseWriter interface.
func (w *StatusLoggingResponseWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *StatusLoggingResponseWriter) Write(data []byte) (int, error) {
	return w.ResponseWriter.Write(data)
}

func (w *StatusLoggingResponseWriter) WriteHeader(statusCode int) {
	// Store the status code.
	w.status = statusCode

	// Write the status code onward.
	w.ResponseWriter.WriteHeader(statusCode)
}

func NewLogrusHandler(handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(rw http.ResponseWriter, r *http.Request) {
		start := time.Now()

		loggingWriter := NewStatusLoggingResponseWriter(rw)
		handler(loggingWriter, r)

		latency := time.Since(start)
		entry := log.WithFields(log.Fields{
			"request":     r.RequestURI,
			"method":      r.Method,
			"remote":      r.RemoteAddr,
			"status":      loggingWriter.Status(),
			"text_status": http.StatusText(loggingWriter.Status()),
			"latency":     latency,
		})

		if reqID := r.Header.Get("X-Request-Id"); reqID != "" {
			entry = entry.WithField("request_id", reqID)
		}
		entry.Info("completed handling request")
	}
}

func NewCombinedHandler(handler func(http.ResponseWriter, *http.Request)) http.Handler {
	return cors.Default().Handler(http.HandlerFunc(NewLogrusHandler(handler)))
}

func main() {
	configFile, err := ioutil.ReadFile("config.yaml")
	if err != nil {
		panic(err)
	}

	var config Config
	err = yaml.Unmarshal(configFile, &config)
	if err != nil {
		panic(err)
	}

	r := mux.NewRouter().StrictSlash(true)

	// Create the reverse proxy paths specified in the config.
	for base, redirectPath := range config.Routes {
		proxy := NewRewriteReverseProxy(fmt.Sprintf("/%s", base), redirectPath)
		r.NewRoute().PathPrefix(fmt.Sprintf("/%s/", base)).Handler(NewCombinedHandler(proxy.ServeHTTP))
	}
	graceful.Run(":8080", 10*time.Second, r)
}
