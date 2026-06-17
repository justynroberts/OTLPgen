package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
)

// version is overridable at build time: -ldflags "-X main.version=v1.2.3"
var version = "dev"

var verbose bool

func logAt(level, format string, a ...any) {
	ts := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(os.Stderr, "%s - otlpgen - %s - %s\n", ts, level, fmt.Sprintf(format, a...))
}

func logInfo(format string, a ...any)  { logAt("INFO", format, a...) }
func logWarn(format string, a ...any)  { logAt("WARNING", format, a...) }
func logError(format string, a ...any) { logAt("ERROR", format, a...) }
func logDebug(format string, a ...any) {
	if verbose {
		logAt("DEBUG", format, a...)
	}
}

// ── Stats ────────────────────────────────────────────────────

type stat struct{ sent, failed int }

type Stats struct {
	logs, metrics, traces stat
	byService             map[string]*stat
}

func (s *Stats) signal(name string) *stat {
	switch name {
	case "logs":
		return &s.logs
	case "metrics":
		return &s.metrics
	case "traces":
		return &s.traces
	}
	return &stat{}
}

func (s *Stats) print(signals []string) {
	logInfo("%s", strings.Repeat("=", 60))
	for _, sig := range signals {
		st := s.signal(sig)
		logInfo("  %s: %d sent, %d failed", strings.ToUpper(sig), st.sent, st.failed)
	}
	logInfo("%s", strings.Repeat("-", 60))
	names := make([]string, 0, len(s.byService))
	for n := range s.byService {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		logInfo("  %s: %d sent, %d failed", n, s.byService[n].sent, s.byService[n].failed)
	}
	var totalSent, totalFailed int
	for _, sig := range signals {
		st := s.signal(sig)
		totalSent += st.sent
		totalFailed += st.failed
	}
	logInfo("  TOTAL: %d sent, %d failed", totalSent, totalFailed)
	logInfo("%s", strings.Repeat("=", 60))
}

// ── Main loop ────────────────────────────────────────────────

func (g *Generator) Run(durationMinutes int, oneShot bool, signals []string) {
	services, ids := g.cfg.EnabledServices()
	if len(services) == 0 {
		logError("No enabled services found in configuration")
		return
	}

	stats := &Stats{byService: map[string]*stat{}}

	logInfo("Starting observability generation for %d services", len(services))
	logInfo("OTLP endpoint: %s", g.cfg.OTLP.URL)
	for _, sig := range signals {
		logInfo("  %-8s → /v1/%s", strings.Title(sig), sig)
	}
	for _, id := range ids {
		name := services[id].Name
		logInfo("  - %s", name)
		stats.byService[name] = &stat{}
	}

	start := time.Now()
	var end time.Time
	if durationMinutes > 0 {
		end = start.Add(time.Duration(durationMinutes) * time.Minute)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	hasLogs := contains(signals, "logs")
	hasMetrics := contains(signals, "metrics")
	hasTraces := contains(signals, "traces")
	lastStats := time.Now()

	for {
		cycleStart := time.Now()

		if hasLogs {
			var entries []logEntry
			for _, id := range ids {
				svc := services[id]
				count := 1
				if oneShot {
					count = svc.RatePerMinute
					if count <= 0 {
						count = 10
					}
				}
				for i := 0; i < count; i++ {
					entries = append(entries, g.generateLogEntry(svc))
				}
			}
			if len(entries) > 0 {
				payload := g.buildOTLPLogs(entries)
				ok := g.client.Send("/v1/logs", payload)
				for _, e := range entries {
					applyStat(&stats.logs, stats.byService[e.serviceName], 1, ok)
				}
			}
		}

		if hasMetrics {
			payload, counts := g.generateMetrics(services, ids)
			if rm, _ := payload["resourceMetrics"].([]any); len(rm) > 0 {
				ok := g.client.Send("/v1/metrics", payload)
				for name, c := range counts {
					applyStat(&stats.metrics, stats.byService[name], c, ok)
				}
			}
		}

		if hasTraces {
			traceCount := 1
			if oneShot {
				traceCount = 5
			}
			for i := 0; i < traceCount; i++ {
				payload, counts := g.generateTrace(services)
				if rs, _ := payload["resourceSpans"].([]any); len(rs) > 0 {
					ok := g.client.Send("/v1/traces", payload)
					for name, c := range counts {
						applyStat(&stats.traces, stats.byService[name], c, ok)
					}
				}
			}
		}

		if time.Since(lastStats) >= time.Minute {
			stats.print(signals)
			lastStats = time.Now()
		}

		if oneShot {
			break
		}
		if !end.IsZero() && time.Now().After(end) {
			logInfo("Duration limit reached (%d minutes)", durationMinutes)
			break
		}

		sleep := time.Second - time.Since(cycleStart)
		if sleep < 0 {
			sleep = 0
		}
		select {
		case <-stop:
			logInfo("Stopping generation...")
			stats.print(signals)
			return
		case <-time.After(sleep):
		}
	}

	stats.print(signals)
}

func applyStat(sig, svc *stat, n int, ok bool) {
	if ok {
		sig.sent += n
		if svc != nil {
			svc.sent += n
		}
	} else {
		sig.failed += n
		if svc != nil {
			svc.failed += n
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// ── CLI ──────────────────────────────────────────────────────

func main() {
	var (
		configPath  = flag.String("config", "", "Path to a YAML override file (deep-merged over the embedded default)")
		duration    = flag.Int("duration", 0, "Duration in minutes (default: run continuously)")
		oneShot     = flag.Bool("one-shot", false, "Send one batch and exit")
		signalsCSV  = flag.String("signals", "logs,metrics,traces", "Comma-separated signals: logs,metrics,traces")
		verboseFlag = flag.Bool("verbose", false, "Enable verbose logging")
		chaos       = flag.String("chaos", "", "Inject chaos: mild, heavy, or extreme")
		printConfig = flag.Bool("print-config", false, "Print the fully resolved (merged + templated) config and exit")
		showVersion = flag.Bool("version", false, "Print version and exit")
	)
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Println("otlpgen", version)
		return
	}

	verbose = *verboseFlag

	cfg, resolved, err := LoadConfig(*configPath)
	if err != nil {
		logError("%v", err)
		os.Exit(1)
	}

	if *printConfig {
		fmt.Print(string(resolved))
		return
	}

	if *chaos != "" {
		if _, ok := chaosPresets[*chaos]; !ok {
			logError("Unknown chaos level: %s. Valid: mild, heavy, extreme", *chaos)
			os.Exit(1)
		}
	}

	signals := splitSignals(*signalsCSV)
	for _, s := range signals {
		if s != "logs" && s != "metrics" && s != "traces" {
			logError("Invalid signal %q. Valid options: logs, metrics, traces", s)
			os.Exit(1)
		}
	}

	if cfg.OTLP.URL == "" {
		logError("OTLP endpoint is empty. Set OTEL_EXPORTER_OTLP_ENDPOINT or otlp.url in your config.")
		os.Exit(1)
	}

	client := NewClient(cfg.OTLP.URL, cfg.OTLP.Headers, cfg.OTLP.verify())
	gen := NewGenerator(cfg, client, *chaos)
	gen.Run(*duration, *oneShot, signals)
}

func splitSignals(csv string) []string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func usage() {
	fmt.Fprint(os.Stderr, `otlpgen — OTLP/HTTP observability data generator (logs, metrics, traces)

Usage:
  otlpgen [flags]

Flags:
  --config PATH     YAML override file, deep-merged over the embedded default
  --signals LIST    Comma-separated: logs,metrics,traces (default: all)
  --duration N      Run for N minutes (default: continuous)
  --one-shot        Send one batch and exit
  --chaos LEVEL     Inject errors/spikes: mild | heavy | extreme
  --print-config    Print the resolved+templated config and exit
  --verbose         Verbose logging
  --version         Print version

Environment:
  OTEL_EXPORTER_OTLP_ENDPOINT   OTLP/HTTP base URL (e.g. https://host:443)
  OTEL_API_KEY                  Token referenced by the default config header

Examples:
  otlpgen                                   # all signals, continuous
  otlpgen --signals logs --one-shot         # one batch of logs
  otlpgen --config acme.yaml --duration 10  # custom platform, 10 minutes
  otlpgen --print-config                     # inspect resolved config
`)
}
