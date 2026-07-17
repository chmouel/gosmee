package gosmee

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"
)

var loadedConfig map[string]any

var commandFlags = map[string]map[string]bool{
	"client": {
		"smee-url":                  true,
		"target-url":                true,
		"output":                    true,
		"log-level":                 true,
		"ignore-event":              true,
		"saveDir":                   true,
		"target-connection-timeout": true,
		"target-retries":            true,
		"noReplay":                  true,
		"nocolor":                   true,
		"insecure-skip-tls-verify":  true,
		"exec":                      true,
		"exec-on-events":            true,
		"exec-env-vars":             true,
		"new-url":                   true,
		"httpie":                    true,
		"channel":                   true,
		"local-debug-url":           true,
		"health-port":               true,
		"sse-buffer-size":           true,
		"encryption-key-file":       true,
		"resume-state-file":         true,
	},
	"replay": {
		"org-repo":                  true,
		"hook-id":                   true,
		"target-url":                true,
		"output":                    true,
		"log-level":                 true,
		"ignore-event":              true,
		"saveDir":                   true,
		"target-connection-timeout": true,
		"target-retries":            true,
		"noReplay":                  true,
		"nocolor":                   true,
		"insecure-skip-tls-verify":  true,
		"exec":                      true,
		"exec-on-events":            true,
		"exec-env-vars":             true,
		"github-token":              true,
		"list-hooks":                true,
		"list-deliveries":           true,
		"time-since":                true,
	},
	"server": {
		"public-url":              true,
		"port":                    true,
		"allowed-ips":             true,
		"trust-proxy":             true,
		"auto-cert":               true,
		"footer":                  true,
		"footer-file":             true,
		"address":                 true,
		"tls-cert":                true,
		"tls-key":                 true,
		"webhook-signature":       true,
		"replay-token":            true,
		"max-body-size":           true,
		"encrypted-channels-file": true,
		"cors-origin":             true,
		"redis-url":               true,
		"redis-stream-maxlen":     true,
	},
	"keygen": {
		"key-file": true,
	},
}

var globalValidKeys = map[string]bool{
	"output":                    true,
	"log-level":                 true,
	"ignore-event":              true,
	"saveDir":                   true,
	"target-connection-timeout": true,
	"target-retries":            true,
	"noReplay":                  true,
	"nocolor":                   true,
	"insecure-skip-tls-verify":  true,
	"exec":                      true,
	"exec-on-events":            true,
	"exec-env-vars":             true,
	"config":                    true,
	"client":                    true,
	"server":                    true,
	"replay":                    true,
	"keygen":                    true,
}

func defaultConfigFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "gosmee", "config.yaml")
}

func normalizeMap(m any) map[string]any {
	res := make(map[string]any)
	if m == nil {
		return res
	}
	if mapStr, ok := m.(map[string]any); ok {
		for k, v := range mapStr {
			res[k] = normalizeValue(v)
		}
		return res
	}
	if mapInter, ok := m.(map[any]any); ok {
		for k, v := range mapInter {
			res[fmt.Sprintf("%v", k)] = normalizeValue(v)
		}
		return res
	}
	return res
}

func normalizeValue(v any) any {
	if m, ok := v.(map[string]any); ok {
		return normalizeMap(m)
	}
	if m, ok := v.(map[any]any); ok {
		return normalizeMap(m)
	}
	if s, ok := v.([]any); ok {
		res := make([]any, len(s))
		for i, item := range s {
			res[i] = normalizeValue(item)
		}
		return res
	}
	return v
}

func validateConfig(cfg map[string]any) error {
	for k, v := range cfg {
		if !globalValidKeys[k] {
			return fmt.Errorf("unknown top-level configuration key: %q", k)
		}
		if vMap, ok := v.(map[string]any); ok {
			validSectionKeys, isSection := commandFlags[k]
			if !isSection {
				return fmt.Errorf("key %q is not a valid section", k)
			}
			for sk := range vMap {
				if !validSectionKeys[sk] {
					return fmt.Errorf("unknown configuration key in section %q: %q", k, sk)
				}
			}
		} else if v != nil {
			if k == "client" || k == "server" || k == "replay" || k == "keygen" {
				return fmt.Errorf("section %q must be a map/dictionary", k)
			}
		}
	}
	return nil
}

func LoadConfig(cCtx *cli.Context) error {
	// urfave/cli already populates "config" from GOSMEE_CONFIG via EnvVars, so
	// cCtx.String("config") covers both --config and the env var.
	configPath := cCtx.String("config")
	explicit := configPath != ""

	if !explicit {
		configPath = defaultConfigFile()
		if configPath == "" {
			return nil
		}
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			return nil
		}
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if explicit {
			return fmt.Errorf("failed to read config file %s: %w", configPath, err)
		}
		return nil
	}

	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("failed to parse YAML config file %s: %w", configPath, err)
	}

	norm := normalizeMap(raw)
	if err := validateConfig(norm); err != nil {
		return fmt.Errorf("invalid config file %s: %w", configPath, err)
	}

	loadedConfig = norm
	return nil
}

func ApplyConfigToContext(cCtx *cli.Context, section string) error {
	if loadedConfig == nil {
		return nil
	}

	merged := make(map[string]any)
	for k, v := range loadedConfig {
		if k != "client" && k != "server" && k != "replay" && k != "keygen" {
			merged[k] = v
		}
	}
	if section != "" {
		if sec, ok := loadedConfig[section].(map[string]any); ok {
			for k, v := range sec {
				merged[k] = v
			}
		}
	}

	for k, v := range merged {
		if k == "smee-url" || k == "target-url" || k == "org-repo" || k == "hook-id" {
			continue
		}

		if !commandFlags[section][k] {
			continue
		}

		if cCtx.IsSet(k) {
			continue
		}

		if v == nil {
			continue
		}

		if slice, ok := v.([]any); ok {
			for _, item := range slice {
				if err := cCtx.Set(k, fmt.Sprintf("%v", item)); err != nil {
					return fmt.Errorf("failed to set slice flag %q from config: %w", k, err)
				}
			}
		} else {
			if err := cCtx.Set(k, fmt.Sprintf("%v", v)); err != nil {
				return fmt.Errorf("failed to set flag %q from config: %w", k, err)
			}
		}
	}

	return nil
}

func GetConfigString(section, key string) string {
	if loadedConfig == nil {
		return ""
	}
	if section != "" {
		if sec, ok := loadedConfig[section].(map[string]any); ok {
			if val, ok := sec[key]; ok {
				return fmt.Sprintf("%v", val)
			}
		}
	}
	if val, ok := loadedConfig[key]; ok {
		return fmt.Sprintf("%v", val)
	}
	return ""
}
