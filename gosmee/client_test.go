package gosmee

import (
	"io"
	"strings"
	"testing"
	"time"

	"golang.org/x/exp/slog"
	"gotest.tools/v3/assert"
)

var simpleJSON = `{
	"x-foo": "bar",
	"user-agent": "gosmee",
	"timestamp": "1650391429188",
	"otherheader": "yolo",
	"content-type": "application/json",
	"x-github-event": "push",
	"body": {"hello": "world"}
}
`

func TestGoSmeeGood(t *testing.T) {
	p := goSmee{
		replayDataOpts: &replayDataOpts{},
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	m, err := p.parse(time.Now().UTC(), []byte(simpleJSON))
	assert.NilError(t, err)
	assert.Equal(t, m.headers["X-Foo"], "bar")
	assert.Equal(t, m.headers["User-Agent"], "gosmee")
	assert.Assert(t, strings.Contains(string(m.body), "hello"))
	assert.Equal(t, m.eventType, "push")
	assert.Equal(t, m.contentType, "application/json")
	assert.Assert(t, strings.HasPrefix(m.timestamp, "2022"))
	_, ok := m.headers["otherheader"]
	assert.Assert(t, !ok)
}

func TestGoSmeeBad(t *testing.T) {
	p := goSmee{
		replayDataOpts: &replayDataOpts{},
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	pm, _ := p.parse(time.Now().UTC(), []byte(`xxxXXXxx`))
	assert.Equal(t, string(pm.body), "")
}

func TestGoSmeeBodyB(t *testing.T) {
	p := goSmee{
		replayDataOpts: &replayDataOpts{},
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	body := `{"bodyB": "eyJoZWxsbyI6ICJ3b3JsZCJ9", "content-type": "application/json"}`
	m, err := p.parse(time.Now().UTC(), []byte(body))
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(m.body), "hello"))
}

func TestGoSmeeBadTimestamp(t *testing.T) {
	p := goSmee{
		replayDataOpts: &replayDataOpts{},
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	json := `{"timestamp": "notanumber", "content-type": "application/json", "body": {}}`
	_, err := p.parse(time.Now().UTC(), []byte(json))
	assert.NilError(t, err)
}

func TestGoSmeeMissingHeaders(t *testing.T) {
	p := goSmee{
		replayDataOpts: &replayDataOpts{},
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	json := `{"body": {}}`
	m, err := p.parse(time.Now().UTC(), []byte(json))
	assert.NilError(t, err)
	assert.Equal(t, len(m.headers), 0)
}

func TestGoSmeeEventID(t *testing.T) {
	p := goSmee{
		replayDataOpts: &replayDataOpts{},
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	json := `{"x-github-delivery": "12345", "content-type": "application/json", "body": {}}`
	m, err := p.parse(time.Now().UTC(), []byte(json))
	assert.NilError(t, err)
	assert.Equal(t, m.eventID, "12345")
}
