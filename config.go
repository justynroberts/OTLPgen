package main

import (
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// defaultConfig is the baseline configuration baked into the binary.
// A --config override file is deep-merged on top of it.
//
//go:embed config/services.yaml
var defaultConfig []byte

// ── Typed configuration ──────────────────────────────────────

type Config struct {
	Vars     map[string]string         `yaml:"vars"`
	OTLP     OTLPConfig                `yaml:"otlp"`
	Services map[string]*ServiceConfig `yaml:"services"`
	Traces   []*TraceTemplate          `yaml:"traces"`
}

type OTLPConfig struct {
	URL         string            `yaml:"url"`
	Environment string            `yaml:"environment"`
	VerifySSL   *bool             `yaml:"verify_ssl"`
	Headers     map[string]string `yaml:"headers"`
}

type ServiceConfig struct {
	Name          string         `yaml:"name"`
	Enabled       *bool          `yaml:"enabled"`
	RatePerMinute int            `yaml:"rate_per_minute"`
	Tags          []string       `yaml:"tags"`
	Hostnames     []string       `yaml:"hostnames"`
	LogPatterns   []*LogPattern  `yaml:"log_patterns"`
	Metrics       []*MetricGroup `yaml:"metrics"`
}

type LogPattern struct {
	Level      string             `yaml:"level"`
	Weight     int                `yaml:"weight"`
	Templates  []string           `yaml:"templates"`
	Attributes map[string][]any   `yaml:"attributes"`
}

type MetricGroup struct {
	Name         string         `yaml:"name"`
	Measurements []*Measurement `yaml:"measurements"`
}

type Measurement struct {
	Name     string   `yaml:"name"`
	Min      float64  `yaml:"min"`
	Max      float64  `yaml:"max"`
	Baseline *float64 `yaml:"baseline"`
	Unit     string   `yaml:"unit"`
}

type TraceTemplate struct {
	Name   string     `yaml:"name"`
	Weight int        `yaml:"weight"`
	Spans  []*SpanDef `yaml:"spans"`
}

type SpanDef struct {
	Service    string    `yaml:"service"`
	Operation  string    `yaml:"operation"`
	Type       string    `yaml:"type"`
	DurationMs float64   `yaml:"duration_ms"`
	Parent     string    `yaml:"parent"`
	Results    []string  `yaml:"results"`
	HTTP       *SpanHTTP `yaml:"http"`
}

type SpanHTTP struct {
	Request struct {
		Method string `yaml:"method"`
	} `yaml:"request"`
	Response struct {
		StatusCode int `yaml:"status_code"`
	} `yaml:"response"`
}

func (s *ServiceConfig) isEnabled() bool { return s.Enabled == nil || *s.Enabled }
func (o *OTLPConfig) verify() bool       { return o.VerifySSL == nil || *o.VerifySSL }

// Version returns the templated platform version, defaulting to 2.1.0.
func (c *Config) Version() string {
	if v, ok := c.Vars["version"]; ok && v != "" {
		return v
	}
	return "2.1.0"
}

// ── Loading: merge → template → decode ───────────────────────

// LoadConfig reads the embedded default, deep-merges an optional override
// file, resolves and substitutes {{vars}} across every string, then decodes
// into a typed Config. Returns the fully-resolved YAML bytes as well so
// callers can print the effective config.
func LoadConfig(overridePath string) (*Config, []byte, error) {
	var base map[string]any
	if err := yaml.Unmarshal(defaultConfig, &base); err != nil {
		return nil, nil, fmt.Errorf("parse embedded config: %w", err)
	}

	if overridePath != "" {
		raw, err := os.ReadFile(overridePath)
		if err != nil {
			return nil, nil, fmt.Errorf("read override %q: %w", overridePath, err)
		}
		var over map[string]any
		if err := yaml.Unmarshal(raw, &over); err != nil {
			return nil, nil, fmt.Errorf("parse override %q: %w", overridePath, err)
		}
		base = deepMerge(base, over)
	}

	vars := resolveVars(extractVars(base))
	substituteTree(base, vars)

	resolved, err := yaml.Marshal(base)
	if err != nil {
		return nil, nil, fmt.Errorf("re-marshal config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(resolved, &cfg); err != nil {
		return nil, nil, fmt.Errorf("decode config: %w", err)
	}

	// Resolve ${ENV} in OTLP endpoint + headers at runtime.
	cfg.OTLP.URL = strings.TrimRight(resolveEnv(cfg.OTLP.URL), "/")
	for k, v := range cfg.OTLP.Headers {
		cfg.OTLP.Headers[k] = resolveEnv(v)
	}
	// The standard OTel SDK var OTEL_EXPORTER_OTLP_HEADERS merges over (and
	// overrides) config headers — so auth can be supplied purely from the env
	// without an override file.
	if env := os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"); env != "" {
		if cfg.OTLP.Headers == nil {
			cfg.OTLP.Headers = map[string]string{}
		}
		for k, v := range parseOTLPHeaders(env) {
			cfg.OTLP.Headers[k] = v
		}
	}
	// Endpoint may also come from the standard OTel var.
	if cfg.OTLP.URL == "" {
		cfg.OTLP.URL = strings.TrimRight(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), "/")
	}
	// Fall back to the map id when a service omits an explicit name.
	for id, svc := range cfg.Services {
		if svc.Name == "" {
			svc.Name = id
		}
	}

	return &cfg, resolved, nil
}

// EnabledServices returns enabled services keyed by their internal id,
// plus the ids sorted for stable output.
func (c *Config) EnabledServices() (map[string]*ServiceConfig, []string) {
	out := map[string]*ServiceConfig{}
	var ids []string
	for id, svc := range c.Services {
		if svc.isEnabled() {
			out[id] = svc
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return out, ids
}

// ── Helpers ──────────────────────────────────────────────────

func deepMerge(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		if bv, ok := out[k]; ok {
			if bm, ok1 := bv.(map[string]any); ok1 {
				if om, ok2 := v.(map[string]any); ok2 {
					out[k] = deepMerge(bm, om)
					continue
				}
			}
		}
		out[k] = v
	}
	return out
}

func extractVars(root map[string]any) map[string]string {
	vars := map[string]string{}
	raw, ok := root["vars"].(map[string]any)
	if !ok {
		return vars
	}
	for k, v := range raw {
		vars[k] = fmt.Sprintf("%v", v)
	}
	return vars
}

// resolveVars expands {{var}} references inside var values until stable,
// so vars may be defined in terms of other vars.
func resolveVars(vars map[string]string) map[string]string {
	for pass := 0; pass < 10; pass++ {
		changed := false
		for k, v := range vars {
			nv := substituteString(v, vars)
			if nv != v {
				vars[k] = nv
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return vars
}

var varRe = regexp.MustCompile(`\{\{\s*([\w.\-]+)\s*\}\}`)

func substituteString(s string, vars map[string]string) string {
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		key := varRe.FindStringSubmatch(m)[1]
		if val, ok := vars[key]; ok {
			return val
		}
		return m // leave unknown placeholders untouched
	})
}

// substituteTree walks the parsed YAML in place, expanding {{vars}} in every
// string value (map values and list elements alike).
func substituteTree(v any, vars map[string]string) any {
	switch t := v.(type) {
	case string:
		return substituteString(t, vars)
	case map[string]any:
		for k, vv := range t {
			t[k] = substituteTree(vv, vars)
		}
		return t
	case []any:
		for i, vv := range t {
			t[i] = substituteTree(vv, vars)
		}
		return t
	default:
		return v
	}
}

var envRe = regexp.MustCompile(`\$\{([^}]+)\}`)

func resolveEnv(s string) string {
	return envRe.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(envRe.FindStringSubmatch(m)[1])
	})
}

// parseOTLPHeaders parses the OTel SDK's OTEL_EXPORTER_OTLP_HEADERS format —
// comma-separated "key=value" pairs with optional whitespace, e.g.
// "Authorization=Basic abc,x-team=42". Per the spec, values are percent-encoded,
// so %XX escapes are decoded; '+' is left as-is so base64 tokens (which use it)
// are not mangled.
func parseOTLPHeaders(s string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if dk, err := url.PathUnescape(k); err == nil {
			k = dk
		}
		if dv, err := url.PathUnescape(v); err == nil {
			v = dv
		}
		if k != "" {
			out[k] = v
		}
	}
	return out
}
