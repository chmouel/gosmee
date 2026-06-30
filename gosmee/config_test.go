package gosmee

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func resetLoadedConfigForTest(t *testing.T) {
	t.Helper()
	old := loadedConfig
	t.Cleanup(func() {
		loadedConfig = old
	})
}

func setLoadedConfigForTest(t *testing.T, cfg map[string]any) {
	t.Helper()
	resetLoadedConfigForTest(t)
	loadedConfig = cfg
}

func newClientContextForTest(t *testing.T, args []string) *cli.Context {
	t.Helper()
	app := cli.NewApp()
	app.Flags = commonFlags
	set := flag.NewFlagSet("test", flag.ContinueOnError)
	flags := mergeFlags(commonFlags, clientFlags...)
	for _, f := range flags {
		assert.NilError(t, f.Apply(set))
	}
	assert.NilError(t, set.Parse(args))
	cCtx := cli.NewContext(app, set, nil)
	cCtx.Command = &cli.Command{
		Name:  "client",
		Flags: flags,
	}
	return cCtx
}

func newReplayContextForTest(t *testing.T, args []string) *cli.Context {
	t.Helper()
	app := cli.NewApp()
	app.Flags = commonFlags
	set := flag.NewFlagSet("test", flag.ContinueOnError)
	flags := mergeFlags(commonFlags, replayFlags...)
	for _, f := range flags {
		assert.NilError(t, f.Apply(set))
	}
	assert.NilError(t, set.Parse(args))
	cCtx := cli.NewContext(app, set, nil)
	cCtx.Command = &cli.Command{
		Name:  "replay",
		Flags: flags,
	}
	return cCtx
}

func TestLoadConfig_Validation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gosmee-test-*")
	assert.NilError(t, err)
	defer os.RemoveAll(tmpDir)

	t.Run("invalid top level key", func(t *testing.T) {
		content := `
invalid_key: value
`
		path := filepath.Join(tmpDir, "config1.yaml")
		err := os.WriteFile(path, []byte(content), 0o644)
		assert.NilError(t, err)

		app := cli.NewApp()
		app.Flags = []cli.Flag{configFlag}
		set := flag.NewFlagSet("test", flag.ContinueOnError)
		_ = configFlag.Apply(set)
		_ = set.Parse([]string{"--config", path})
		cCtx := cli.NewContext(app, set, nil)

		err = LoadConfig(cCtx)
		assert.ErrorContains(t, err, `unknown top-level configuration key`)
	})

	t.Run("invalid section key", func(t *testing.T) {
		content := `
client:
  invalid_flag: value
`
		path := filepath.Join(tmpDir, "config2.yaml")
		err := os.WriteFile(path, []byte(content), 0o644)
		assert.NilError(t, err)

		app := cli.NewApp()
		app.Flags = []cli.Flag{configFlag}
		set := flag.NewFlagSet("test", flag.ContinueOnError)
		_ = configFlag.Apply(set)
		_ = set.Parse([]string{"--config", path})
		cCtx := cli.NewContext(app, set, nil)

		err = LoadConfig(cCtx)
		assert.ErrorContains(t, err, `unknown configuration key in section "client"`)
	})

	t.Run("invalid section type", func(t *testing.T) {
		content := `
client: not_a_map
`
		path := filepath.Join(tmpDir, "config3.yaml")
		err := os.WriteFile(path, []byte(content), 0o644)
		assert.NilError(t, err)

		app := cli.NewApp()
		app.Flags = []cli.Flag{configFlag}
		set := flag.NewFlagSet("test", flag.ContinueOnError)
		_ = configFlag.Apply(set)
		_ = set.Parse([]string{"--config", path})
		cCtx := cli.NewContext(app, set, nil)

		err = LoadConfig(cCtx)
		assert.ErrorContains(t, err, `section "client" must be a map/dictionary`)
	})

	t.Run("valid config", func(t *testing.T) {
		content := `
saveDir: "/tmp/common"
output: json
client:
  smee-url: "https://smee.io/abc"
  target-url: "http://localhost:8080"
  sse-buffer-size: 524288
  resume-state-file: "/tmp/gosmee.resume"
server:
  port: 8080
  redis-url: redis://redis.example.com:6379/0
  redis-stream-maxlen: 5000
`
		path := filepath.Join(tmpDir, "config4.yaml")
		err := os.WriteFile(path, []byte(content), 0o644)
		assert.NilError(t, err)

		app := cli.NewApp()
		app.Flags = []cli.Flag{configFlag}
		set := flag.NewFlagSet("test", flag.ContinueOnError)
		_ = configFlag.Apply(set)
		_ = set.Parse([]string{"--config", path})
		cCtx := cli.NewContext(app, set, nil)

		err = LoadConfig(cCtx)
		assert.NilError(t, err)

		assert.Equal(t, GetConfigString("", "saveDir"), "/tmp/common")
		assert.Equal(t, GetConfigString("", "output"), "json")
		assert.Equal(t, GetConfigString("client", "smee-url"), "https://smee.io/abc")
		assert.Equal(t, GetConfigString("client", "target-url"), "http://localhost:8080")
		assert.Equal(t, GetConfigString("client", "sse-buffer-size"), "524288")
		assert.Equal(t, GetConfigString("client", "resume-state-file"), "/tmp/gosmee.resume")
		assert.Equal(t, GetConfigString("server", "port"), "8080")
		assert.Equal(t, GetConfigString("server", "redis-url"), "redis://redis.example.com:6379/0")
		assert.Equal(t, GetConfigString("server", "redis-stream-maxlen"), "5000")
	})
}

func TestReplayConfigCommandLifecycle(t *testing.T) {
	resetLoadedConfigForTest(t)
	tmpDir, err := os.MkdirTemp("", "gosmee-test-*")
	assert.NilError(t, err)
	defer os.RemoveAll(tmpDir)

	writeConfig := func(t *testing.T, name, content string) string {
		t.Helper()
		path := filepath.Join(tmpDir, name)
		err := os.WriteFile(path, []byte(content), 0o644)
		assert.NilError(t, err)
		return path
	}

	t.Run("github token from config is accepted after before hook", func(t *testing.T) {
		path := writeConfig(t, "token.yaml", `
replay:
  github-token: dummy
  org-repo: chmouel/gosmee
`)
		err := makeapp().Run([]string{"gosmee", "replay", "--config", path})
		assert.ErrorContains(t, err, "hook-id is required")
		assert.Assert(t, !strings.Contains(err.Error(), "github-token"))
	})

	t.Run("missing github token still errors clearly", func(t *testing.T) {
		path := writeConfig(t, "missing-token.yaml", `
replay:
  org-repo: chmouel/gosmee
`)
		err := makeapp().Run([]string{"gosmee", "replay", "--config", path})
		assert.ErrorContains(t, err, `required flag "github-token" not set`)
	})

	t.Run("false list hooks config does not enable list mode", func(t *testing.T) {
		path := writeConfig(t, "list-hooks-false.yaml", `
replay:
  github-token: dummy
  org-repo: chmouel/gosmee
  list-hooks: false
`)
		err := makeapp().Run([]string{"gosmee", "replay", "--config", path})
		assert.ErrorContains(t, err, "hook-id is required")
	})
}

func TestReplayConfigPrecedence(t *testing.T) {
	setLoadedConfigForTest(t, map[string]any{
		"replay": map[string]any{
			"github-token": "config-token",
		},
	})
	cCtx := newReplayContextForTest(t, []string{"--github-token", "cli-token"})
	err := ApplyConfigToContext(cCtx, "replay")
	assert.NilError(t, err)
	assert.Equal(t, cCtx.String("github-token"), "cli-token")
}

func TestResolveClientURLsPrecedence(t *testing.T) {
	t.Run("cli positional urls win", func(t *testing.T) {
		t.Setenv("GOSMEE_URL", "https://env.example")
		t.Setenv("GOSMEE_TARGET_URL", "http://env-target")
		setLoadedConfigForTest(t, map[string]any{
			"client": map[string]any{
				"smee-url":   "https://config.example",
				"target-url": "http://config-target",
			},
		})
		cCtx := newClientContextForTest(t, []string{"https://cli.example", "http://cli-target"})

		smeeURL, targetURL, noReplay, err := resolveClientURLs(cCtx)
		assert.NilError(t, err)
		assert.Equal(t, smeeURL, "https://cli.example")
		assert.Equal(t, targetURL, "http://cli-target")
		assert.Equal(t, noReplay, false)
	})

	t.Run("env urls override config independently", func(t *testing.T) {
		t.Setenv("GOSMEE_URL", "https://env.example")
		t.Setenv("GOSMEE_TARGET_URL", "http://env-target")
		setLoadedConfigForTest(t, map[string]any{
			"client": map[string]any{
				"smee-url":   "https://config.example",
				"target-url": "http://config-target",
			},
		})
		cCtx := newClientContextForTest(t, nil)

		smeeURL, targetURL, noReplay, err := resolveClientURLs(cCtx)
		assert.NilError(t, err)
		assert.Equal(t, smeeURL, "https://env.example")
		assert.Equal(t, targetURL, "http://env-target")
		assert.Equal(t, noReplay, false)
	})

	t.Run("single env url overrides matching config value", func(t *testing.T) {
		t.Setenv("GOSMEE_URL", "https://env.example")
		setLoadedConfigForTest(t, map[string]any{
			"client": map[string]any{
				"smee-url":   "https://config.example",
				"target-url": "http://config-target",
			},
		})
		cCtx := newClientContextForTest(t, nil)

		smeeURL, targetURL, _, err := resolveClientURLs(cCtx)
		assert.NilError(t, err)
		assert.Equal(t, smeeURL, "https://env.example")
		assert.Equal(t, targetURL, "http://config-target")
	})

	t.Run("config only urls work", func(t *testing.T) {
		setLoadedConfigForTest(t, map[string]any{
			"client": map[string]any{
				"smee-url":   "https://config.example",
				"target-url": "http://config-target",
			},
		})
		cCtx := newClientContextForTest(t, nil)

		smeeURL, targetURL, _, err := resolveClientURLs(cCtx)
		assert.NilError(t, err)
		assert.Equal(t, smeeURL, "https://config.example")
		assert.Equal(t, targetURL, "http://config-target")
	})

	t.Run("config smee url with exec does not require target", func(t *testing.T) {
		setLoadedConfigForTest(t, map[string]any{
			"client": map[string]any{
				"smee-url": "https://config.example",
				"exec":     "echo ok",
			},
		})
		cCtx := newClientContextForTest(t, nil)
		err := ApplyConfigToContext(cCtx, "client")
		assert.NilError(t, err)

		smeeURL, targetURL, noReplay, err := resolveClientURLs(cCtx)
		assert.NilError(t, err)
		assert.Equal(t, smeeURL, "https://config.example")
		assert.Equal(t, targetURL, "")
		assert.Equal(t, noReplay, true)
	})
}

func TestApplyConfigToContext(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gosmee-test-*")
	assert.NilError(t, err)
	defer os.RemoveAll(tmpDir)

	content := `
saveDir: "/tmp/common"
output: pretty
client:
  saveDir: "/tmp/client"
  sse-buffer-size: 524288
  ignore-event:
    - push
    - pull_request
`
	path := filepath.Join(tmpDir, "config.yaml")
	err = os.WriteFile(path, []byte(content), 0o644)
	assert.NilError(t, err)

	// Create urfave/cli context simulating client command
	app := cli.NewApp()
	app.Flags = commonFlags

	t.Run("config sets unset flags with section override", func(t *testing.T) {
		set := flag.NewFlagSet("test", flag.ContinueOnError)
		// Simulating flags for client command.
		for _, f := range mergeFlags(commonFlags, clientFlags...) {
			_ = f.Apply(set)
		}
		// Parse CLI arguments: only "--config" is passed.
		_ = set.Parse([]string{"--config", path})
		cCtx := cli.NewContext(app, set, nil)
		cCtx.Command = &cli.Command{
			Name:  "client",
			Flags: mergeFlags(commonFlags, clientFlags...),
		}

		err := LoadConfig(cCtx)
		assert.NilError(t, err)

		err = ApplyConfigToContext(cCtx, "client")
		assert.NilError(t, err)

		assert.Equal(t, cCtx.String("saveDir"), "/tmp/client") // Overridden by section
		assert.Equal(t, cCtx.String("output"), "pretty")       // From top level
		assert.Equal(t, cCtx.Int("sse-buffer-size"), 524288)   // Section specific
		assert.DeepEqual(t, cCtx.StringSlice("ignore-event"), []string{"push", "pull_request"})
	})

	t.Run("cli arguments take precedence", func(t *testing.T) {
		set := flag.NewFlagSet("test", flag.ContinueOnError)
		for _, f := range mergeFlags(commonFlags, clientFlags...) {
			_ = f.Apply(set)
		}
		// Parse CLI: pass explicit "--saveDir" and "--output" and "--ignore-event".
		_ = set.Parse([]string{"--config", path, "--saveDir", "/tmp/cli", "--output", "json", "--ignore-event", "release"})
		cCtx := cli.NewContext(app, set, nil)
		cCtx.Command = &cli.Command{
			Name:  "client",
			Flags: mergeFlags(commonFlags, clientFlags...),
		}

		err := LoadConfig(cCtx)
		assert.NilError(t, err)

		err = ApplyConfigToContext(cCtx, "client")
		assert.NilError(t, err)

		assert.Equal(t, cCtx.String("saveDir"), "/tmp/cli") // CLI wins
		assert.Equal(t, cCtx.String("output"), "json")      // CLI wins
		assert.DeepEqual(t, cCtx.StringSlice("ignore-event"), []string{"release"})
	})
}
