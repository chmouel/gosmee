package gosmee

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/mgutz/ansi"
	"github.com/r3labs/sse/v2"
	"github.com/urfave/cli/v2"
)

var (
	defaultServerPort    = 3333
	defaultServerAddress = "localhost"
)

//go:embed templates/index.tmpl
var indexTmpl []byte

func errorIt(w http.ResponseWriter, r *http.Request, status int, err error) {
	w.WriteHeader(status)
	_, _ = w.Write([]byte(err.Error()))
}

func serve(c *cli.Context) error {
	publicURL := c.String("public-url")
	router := chi.NewRouter()
	router.Use(middleware.Logger)
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)

	events := sse.New()
	events.AutoReplay = false
	events.AutoStream = true
	events.OnSubscribe = (func(sid string, sub *sse.Subscriber) {
		events.Publish(sid, &sse.Event{
			Data: []byte("ready"),
		})
	})

	router.Get("/", func(w http.ResponseWriter, r *http.Request) {
		url := fmt.Sprintf("%s/%s", publicURL, randomString(12))
		w.WriteHeader(http.StatusOK)
		// parse template  file in indexTmpl
		t, err := template.New("index").Parse(string(indexTmpl))
		if err != nil {
			errorIt(w, r, http.StatusInternalServerError, err)
			return
		}
		varmap := map[string]string{
			"URL": url,
		}
		w.Header().Set("Content-Type", "text/html")
		if err := t.ExecuteTemplate(w, "index", varmap); err != nil {
			errorIt(w, r, http.StatusInternalServerError, err)
			return
		}
	})

	router.Get("/new", func(w http.ResponseWriter, r *http.Request) {
		url := fmt.Sprintf("%s%s\n", publicURL,
			strings.ReplaceAll(r.URL.String(), "/new", fmt.Sprintf("/%s",
				randomString(12))))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(url))
	})
	router.Get("/{channel:[a-zA-Z0-9]{12,}}", func(w http.ResponseWriter, r *http.Request) {
		channel := chi.URLParam(r, "channel")
		ua := r.Header.Get("User-Agent")
		if !strings.HasPrefix(ua, "gosmee") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(
				fmt.Sprintf("Use the gosmee client to connect, eg: gosmee client %s https://yourlocalservice\n", publicURL)))
			return
		}
		newURL, err := r.URL.Parse(fmt.Sprintf("%s?stream=%s", r.URL.Path, channel))
		if err != nil {
			errorIt(w, r, http.StatusInternalServerError, err)
			return
		}
		r.URL = newURL
		events.ServeHTTP(w, r)
	})
	router.Post("/{channel:[a-zA-Z0-9]{12,}}", func(w http.ResponseWriter, r *http.Request) {
		channel := chi.URLParam(r, "channel")
		// try to json decode body
		var d interface{}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			errorIt(w, r, http.StatusInternalServerError, err)
			return
		}
		if err := json.Unmarshal(body, &d); err != nil {
			errorIt(w, r, http.StatusBadRequest, err)
			return
		}
		// check if we have content-type json
		if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			errorIt(w, r, http.StatusBadRequest, fmt.Errorf("content-type must be application/json"))
			return
		}
		// convert headers to map[string]string
		payload := map[string]interface{}{}
		for k, v := range r.Header {
			payload[strings.ToLower(k)] = v[0]
		}
		// easier with base64 for server instead of string
		bodyEncoded := base64.StdEncoding.EncodeToString(body)
		timestamp := time.Now().UTC().UnixMilli()
		payload["timestamp"] = timestamp
		payload["bodyB"] = bodyEncoded
		reencoded, err := json.Marshal(payload)
		if err != nil {
			errorIt(w, r, http.StatusInternalServerError, err)
			return
		}
		events.CreateStream(channel)
		events.Publish(channel, &sse.Event{
			Data: reencoded,
		})
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"status":%d, "channel": "%s", "message":"ok"}`, http.StatusAccepted, channel)))
	})
	config := goSmee{}

	certFile := c.String("tls-cert")
	certKey := c.String("tls-key")
	sslEnabled := certFile != "" && certKey != ""
	portAddr := fmt.Sprintf("%s:%d", c.String("address"), c.Int("port"))
	if publicURL == "" {
		publicURL = "http://"
		if sslEnabled {
			publicURL = "https://"
		}
		publicURL = fmt.Sprintf("%s%s", publicURL, portAddr)
	}

	os.Stdout.WriteString(
		fmt.Sprintf("%sServing for webhooks on %s\n",
			config.emoji("✓", "yellow+b"),
			ansi.Color(publicURL, "green+u")))

	if sslEnabled {
		//nolint:gosec
		return http.ListenAndServeTLS(portAddr, certFile, certKey, router)
	}
	//nolint:gosec
	return http.ListenAndServe(portAddr, router)
}
