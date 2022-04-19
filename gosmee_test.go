package main

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
	_, ok := m.headers["otherheader"]
	assert.Assert(t, !ok)
}

func TestGoSmeeBad(t *testing.T) {
	p := goSmee{}
	pm, _ := p.parse([]byte(`xxxXXXxx`))
	assert.Equal(t, string(pm.body), "")
}
