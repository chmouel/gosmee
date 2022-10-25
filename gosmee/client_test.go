package gosmee

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

var simpleJSON = `{
	"x-foo": "bar",
	"user-agent": "gosmee",
	"timestamp": 1650391429188,
	"otherheader": "yolo",
	"content-type": "application/json",
	"x-github-event": "push",
	"body": {"hello": "world"}
}
`

func TestGoSmeeGood(t *testing.T) {
	p := goSmee{}
	m, err := p.parse([]byte(simpleJSON))
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
	p := goSmee{}
	pm, _ := p.parse([]byte(`xxxXXXxx`))
	assert.Equal(t, string(pm.body), "")
}
