package main

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"
)

// severityMap maps log levels to OTLP severity numbers.
var severityMap = map[string]int{
	"debug": 5,
	"info":  9,
	"warn":  13,
	"error": 17,
}

// chaosPresets: error-weight, failure-trace-weight, metric-spike multipliers.
var chaosPresets = map[string][3]float64{
	"mild":    {2.0, 2.0, 1.3},
	"heavy":   {5.0, 5.0, 1.8},
	"extreme": {20.0, 20.0, 2.5},
}

func epochNs() int64 { return time.Now().UnixNano() }

// ── Generic random helpers ───────────────────────────────────

func randChoice[T any](items []T) T {
	return items[rand.Intn(len(items))]
}

func weightedPick[T any](items []T, weight func(T) int) T {
	total := 0
	for _, it := range items {
		if w := weight(it); w > 0 {
			total += w
		}
	}
	if total <= 0 {
		return items[rand.Intn(len(items))]
	}
	r := rand.Intn(total)
	for _, it := range items {
		w := weight(it)
		if w < 0 {
			w = 0
		}
		if r < w {
			return it
		}
		r -= w
	}
	return items[len(items)-1]
}

func toStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// ── OTLP attribute helpers ───────────────────────────────────

func attrStr(k, v string) map[string]any {
	return map[string]any{"key": k, "value": map[string]any{"stringValue": v}}
}

func attrInt(k string, v int) map[string]any {
	// OTLP/JSON encodes int64 values as strings.
	return map[string]any{"key": k, "value": map[string]any{"intValue": strconv.Itoa(v)}}
}

// ── Generator ────────────────────────────────────────────────

type Generator struct {
	cfg     *Config
	client  *Client
	chaos   string
	version string
}

func NewGenerator(cfg *Config, client *Client, chaos string) *Generator {
	g := &Generator{cfg: cfg, client: client, chaos: chaos, version: cfg.Version()}
	if chaos != "" {
		g.applyChaos(chaos)
	}
	return g
}

func (g *Generator) applyChaos(level string) {
	mult, ok := chaosPresets[level]
	if !ok {
		return
	}
	errMult, traceMult, metricMult := mult[0], mult[1], mult[2]
	logWarn("CHAOS MODE [%s] — err x%.1f, failure-traces x%.1f, metrics x%.1f",
		level, errMult, traceMult, metricMult)

	for _, svc := range g.cfg.Services {
		for _, p := range svc.LogPatterns {
			if p.Level == "error" {
				p.Weight = int(float64(p.Weight) * errMult)
			}
		}
		for _, group := range svc.Metrics {
			for _, m := range group.Measurements {
				name := strings.ToLower(m.Name)
				if matchesChaosMetric(name) {
					if m.Baseline != nil {
						spiked := *m.Baseline * metricMult
						capped := m.Max
						if *m.Baseline*10 < capped {
							capped = *m.Baseline * 10
						}
						if spiked > capped {
							spiked = capped
						}
						*m.Baseline = spiked
					}
				}
			}
		}
	}

	for _, tr := range g.cfg.Traces {
		name := strings.ToLower(tr.Name)
		if strings.Contains(name, "failure") || strings.Contains(name, "slow") || strings.Contains(name, "error") {
			tr.Weight = int(float64(tr.Weight) * traceMult)
		}
	}
}

func matchesChaosMetric(name string) bool {
	for _, k := range []string{"error", "latency", "cpu", "memory", "queue", "mismatch", "failure"} {
		if strings.Contains(name, k) {
			return true
		}
	}
	return false
}

func (g *Generator) resource(serviceName, hostname string) map[string]any {
	return map[string]any{
		"attributes": []any{
			attrStr("service.name", serviceName),
			attrStr("service.version", g.version),
			attrStr("deployment.environment", g.cfg.OTLP.Environment),
			attrStr("host.name", hostname),
			attrStr("telemetry.sdk.name", "otlpgen"),
			attrStr("telemetry.sdk.language", "go"),
		},
	}
}

func labelsFromTags(tags []string) [][2]string {
	out := make([][2]string, 0, len(tags))
	for _, tag := range tags {
		if k, v, ok := strings.Cut(tag, ":"); ok {
			out = append(out, [2]string{k, v})
		}
	}
	return out
}

// ── Logs ─────────────────────────────────────────────────────

type logEntry struct {
	serviceName string
	hostname    string
	record      map[string]any
}

func (g *Generator) generateLogEntry(svc *ServiceConfig) logEntry {
	pattern := weightedPick(svc.LogPatterns, func(p *LogPattern) int { return p.Weight })
	template := randChoice(pattern.Templates)
	hostname := randChoice(svc.Hostnames)

	attrs := map[string]string{}
	for key, values := range pattern.Attributes {
		if strings.Contains(template, "{"+key+"}") && len(values) > 0 {
			attrs[key] = toStr(randChoice(values))
		}
	}

	message := template
	recordAttrs := make([]any, 0, len(attrs)+len(svc.Tags))
	for k, v := range attrs {
		message = strings.ReplaceAll(message, "{"+k+"}", v)
		recordAttrs = append(recordAttrs, attrStr(k, v))
	}
	for _, kv := range labelsFromTags(svc.Tags) {
		recordAttrs = append(recordAttrs, attrStr(kv[0], kv[1]))
	}

	level := pattern.Level
	sev := severityMap[level]
	if sev == 0 {
		sev = 9
	}

	return logEntry{
		serviceName: svc.Name,
		hostname:    hostname,
		record: map[string]any{
			"timeUnixNano":   strconv.FormatInt(epochNs(), 10),
			"severityNumber": sev,
			"severityText":   strings.ToUpper(level),
			"body":           map[string]any{"stringValue": message},
			"attributes":     recordAttrs,
		},
	}
}

func (g *Generator) buildOTLPLogs(entries []logEntry) map[string]any {
	type bucket struct {
		hostname string
		records  []any
	}
	order := []string{}
	byService := map[string]*bucket{}
	for _, e := range entries {
		b, ok := byService[e.serviceName]
		if !ok {
			b = &bucket{hostname: e.hostname}
			byService[e.serviceName] = b
			order = append(order, e.serviceName)
		}
		b.records = append(b.records, e.record)
	}

	resourceLogs := make([]any, 0, len(order))
	for _, svc := range order {
		b := byService[svc]
		resourceLogs = append(resourceLogs, map[string]any{
			"resource": g.resource(svc, b.hostname),
			"scopeLogs": []any{map[string]any{
				"scope":      map[string]any{"name": "otlpgen", "version": "1.0.0"},
				"logRecords": b.records,
			}},
		})
	}
	return map[string]any{"resourceLogs": resourceLogs}
}

// ── Metrics ──────────────────────────────────────────────────

// generateMetrics returns the payload and a per-service metric count.
func (g *Generator) generateMetrics(services map[string]*ServiceConfig, ids []string) (map[string]any, map[string]int) {
	resourceMetrics := []any{}
	counts := map[string]int{}

	for _, id := range ids {
		svc := services[id]
		if len(svc.Metrics) == 0 {
			continue
		}
		hostname := randChoice(svc.Hostnames)
		ts := strconv.FormatInt(epochNs(), 10)
		scopeMetrics := []any{}
		count := 0

		for _, group := range svc.Metrics {
			metricsList := []any{}
			for _, m := range group.Measurements {
				base := (m.Min + m.Max) / 2
				if m.Baseline != nil {
					base = *m.Baseline
				}
				jitter := (m.Max - m.Min) * 0.15
				value := base + (rand.Float64()*2-1)*jitter
				if value < m.Min {
					value = m.Min
				}
				if value > m.Max {
					value = m.Max
				}
				value = roundTo(value, 2)

				metricsList = append(metricsList, map[string]any{
					"name": m.Name,
					"unit": m.Unit,
					"gauge": map[string]any{
						"dataPoints": []any{map[string]any{
							"timeUnixNano": ts,
							"asDouble":     value,
							"attributes":   []any{attrStr("metricset", group.Name)},
						}},
					},
				})
				count++
			}
			scopeMetrics = append(scopeMetrics, map[string]any{
				"scope":   map[string]any{"name": "otlpgen." + group.Name, "version": "1.0.0"},
				"metrics": metricsList,
			})
		}

		resourceMetrics = append(resourceMetrics, map[string]any{
			"resource":     g.resource(svc.Name, hostname),
			"scopeMetrics": scopeMetrics,
		})
		counts[svc.Name] = count
	}

	return map[string]any{"resourceMetrics": resourceMetrics}, counts
}

func roundTo(v float64, places int) float64 {
	p := 1.0
	for i := 0; i < places; i++ {
		p *= 10
	}
	return float64(int64(v*p+0.5*sign(v))) / p
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

// ── Traces ───────────────────────────────────────────────────

func (g *Generator) generateTrace(services map[string]*ServiceConfig) (map[string]any, map[string]int) {
	if len(g.cfg.Traces) == 0 {
		return map[string]any{"resourceSpans": []any{}}, nil
	}

	tmpl := weightedPick(g.cfg.Traces, func(t *TraceTemplate) int {
		if t.Weight <= 0 {
			return 1
		}
		return t.Weight
	})

	traceID := randomHex(32)
	tsBase := epochNs()

	spanIDs := make([]string, len(tmpl.Spans))
	serviceToIndex := map[string]int{}
	for i, sd := range tmpl.Spans {
		spanIDs[i] = randomHex(16)
		serviceToIndex[sd.Service] = i
	}

	type bucket struct {
		serviceName string
		spans       []any
	}
	order := []string{}
	byService := map[string]*bucket{}
	counts := map[string]int{}
	var offset int64

	for i, sd := range tmpl.Spans {
		svc, ok := services[sd.Service]
		if !ok {
			continue
		}

		jitter := sd.DurationMs * 0.3
		durMs := sd.DurationMs + (rand.Float64()*2-1)*jitter
		if durMs < 1 {
			durMs = 1
		}
		durNs := int64(durMs * 1_000_000)
		start := tsBase + offset
		end := start + durNs

		result := "success"
		if len(sd.Results) > 0 {
			result = randChoice(sd.Results)
		}
		statusCode := 1
		if result != "success" {
			statusCode = 2
		}

		kind := 3
		if sd.Type == "request" {
			kind = 2
		}

		spanAttrs := []any{attrStr("span.type", orDefault(sd.Type, "custom"))}
		if sd.HTTP != nil {
			method := sd.HTTP.Request.Method
			if method == "" {
				method = "GET"
			}
			status := sd.HTTP.Response.StatusCode
			if status == 0 {
				status = 200
			}
			spanAttrs = append(spanAttrs,
				attrStr("http.request.method", method),
				attrInt("http.response.status_code", status),
			)
			if parts := strings.SplitN(sd.Operation, " ", 2); len(parts) == 2 {
				spanAttrs = append(spanAttrs, attrStr("url.path", parts[1]))
			}
		}

		span := map[string]any{
			"traceId":           traceID,
			"spanId":            spanIDs[i],
			"name":              sd.Operation,
			"kind":              kind,
			"startTimeUnixNano": strconv.FormatInt(start, 10),
			"endTimeUnixNano":   strconv.FormatInt(end, 10),
			"status":            map[string]any{"code": statusCode, "message": result},
			"attributes":        spanAttrs,
		}
		if sd.Parent != "" {
			if pi, ok := serviceToIndex[sd.Parent]; ok {
				span["parentSpanId"] = spanIDs[pi]
			}
		}

		b, ok := byService[sd.Service]
		if !ok {
			b = &bucket{serviceName: svc.Name}
			byService[sd.Service] = b
			order = append(order, sd.Service)
		}
		b.spans = append(b.spans, span)
		counts[svc.Name]++
		offset += int64(float64(durNs) * 0.1)
	}

	resourceSpans := make([]any, 0, len(order))
	for _, id := range order {
		b := byService[id]
		hostname := randChoice(services[id].Hostnames)
		resourceSpans = append(resourceSpans, map[string]any{
			"resource": g.resource(b.serviceName, hostname),
			"scopeSpans": []any{map[string]any{
				"scope": map[string]any{"name": "otlpgen", "version": "1.0.0"},
				"spans": b.spans,
			}},
		})
	}

	return map[string]any{"resourceSpans": resourceSpans}, counts
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

const hexDigits = "0123456789abcdef"

func randomHex(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = hexDigits[rand.Intn(16)]
	}
	return string(b)
}
