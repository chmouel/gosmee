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
	"github.com/r3labs/sse/v2"
	"github.com/urfave/cli/v2"
	"golang.org/x/crypto/acme/autocert"
)

var (
	defaultServerPort    = 3333
	defaultServerAddress = "localhost"
)

//go:embed templates/index.tmpl
var indexTmpl []byte

func errorIt(w http.ResponseWriter, _ *http.Request, status int, err error) {
	w.WriteHeader(status)
	_, _ = w.Write([]byte(err.Error()))
}

func serve(c *cli.Context) error {
	publicURL := c.String("public-url")
	footer := c.String("footer")
	footerFile := c.String("footer-file")
	if footer != "" && footerFile != "" {
		return fmt.Errorf("cannot use both --footer and --footer-file")
	}
	if footerFile != "" {
		b, err := os.ReadFile(footerFile)
		if err != nil {
			return err
		}
		footer = string(b)
	}
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)

	events := sse.New()
	events.AutoReplay = false
	events.AutoStream = true
	events.OnSubscribe = (func(sid string, _ *sse.Subscriber) {
		events.Publish(sid, &sse.Event{
			Data: []byte("ready"),
		})
	})

	router.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		// redirect to /new
		w.Header().Set("Location", fmt.Sprintf("%s/%s", publicURL, randomString(12)))
		w.WriteHeader(http.StatusFound)
	})

	router.Get("/new", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", fmt.Sprintf("%s/%s", publicURL, randomString(12)))
		w.WriteHeader(http.StatusFound)
	})

	router.Get("/{channel:[a-zA-Z0-9-_]{12,}}", func(w http.ResponseWriter, r *http.Request) {
		channel := chi.URLParam(r, "channel")
		accept := r.Header.Get("User-Agent")
		if !strings.Contains(accept, "gosmee") {
			w.WriteHeader(http.StatusOK)

			url := fmt.Sprintf("%s/%s", publicURL, channel)
			w.WriteHeader(http.StatusOK)
			t, err := template.New("index").Parse(string(indexTmpl))
			if err != nil {
				errorIt(w, r, http.StatusInternalServerError, err)
				return
			}
			varmap := map[string]string{
				"URL":     url,
				"Version": string(Version),
				"Footer":  footer,
			}
			w.Header().Set("Content-Type", "text/html")
			if err := t.ExecuteTemplate(w, "index", varmap); err != nil {
				errorIt(w, r, http.StatusInternalServerError, err)
			}
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
		// grab current time stamp before we take any further actions
		now := time.Now().UTC()
		// check if we have content-type json
		if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			errorIt(w, r, http.StatusBadRequest, fmt.Errorf("content-type must be application/json"))
			return
		}
		channel := chi.URLParam(r, "channel")
		// try to json decode body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			errorIt(w, r, http.StatusInternalServerError, err)
			return
		}
		var d interface{}
		if err := json.Unmarshal(body, &d); err != nil {
			errorIt(w, r, http.StatusBadRequest, err)
			return
		}
		headers := ""
		payload := map[string]interface{}{}
		for k, v := range r.Header {
			headers += fmt.Sprintf(" %s=%s", k, v)
			payload[strings.ToLower(k)] = v[0]
		}
		// easier with base64 for server instead of string
		payload["timestamp"] = fmt.Sprintf("%d", now.UnixMilli())
		payload["bodyB"] = base64.StdEncoding.EncodeToString(body)
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

		fmt.Fprintf(w, "{\"status\": %d, \"channel\": \"%s\", \"message\": \"ok\"}\n", http.StatusAccepted, channel)
		fmt.Fprintf(os.Stdout, "%s Published %s%s on channel %s\n", now.Format("2006-01-02T15.04.01.000"), middleware.GetReqID(r.Context()), headers, channel)
	})

	autoCert := c.Bool("auto-cert")
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

	fmt.Fprintf(os.Stdout, "Serving for webhooks on %s\n", publicURL)

	if sslEnabled {
		//nolint:gosec
		return http.ListenAndServeTLS(portAddr, certFile, certKey, router)
	} else if autoCert {
		//nolint: gosec
		return http.Serve(autocert.NewListener(publicURL), router)
	}
	//nolint:gosec
	return http.ListenAndServe(portAddr, router)
}
