package files

import (
	"fmt"
	"strings"
)

// Kind is what a query's datasource speaks.
type Kind int

const (
	Metrics Kind = iota // MetricsQL (VictoriaMetrics)
	Logs                // LogsQL (VictoriaLogs)
	Loki                // a Loki-type datasource: refused (logs are LogsQL)
	Traces              // Tempo TraceQL; other trace plugins remain classified but are skipped
	Other               // expressions, annotations, Grafana's own source: nothing to parse
)

func (k Kind) String() string {
	return [...]string{"metrics", "logs", "loki", "traces", "other"}[k]
}

// Classify tells the kind of a datasource from its plugin type, else from its uid
// (a platform pins its datasource uids: fixture-metrics, fixture-logs, mimir ...).
// An error names what could not be told.
func Classify(dsType, dsUID string) (Kind, error) {
	t := strings.ToLower(dsType)
	switch {
	case t == "":
	case t == "loki":
		return Loki, nil
	case t == "prometheus", strings.HasPrefix(t, "victoriametrics-metrics"), t == "victoriametrics-datasource":
		return Metrics, nil
	case strings.Contains(t, "logs"):
		return Logs, nil
	case t == "tempo", t == "jaeger", t == "zipkin":
		return Traces, nil
	case t == "grafana-pyroscope-datasource", t == "grafana", t == "mixed", t == "__expr__", t == "datasource":
		return Other, nil
	default:
		return Other, fmt.Errorf("datasource type %q is not one obs knows (prometheus, victoriametrics-logs-datasource, loki, tempo, ...)", dsType)
	}
	u := strings.ToLower(dsUID)
	switch {
	case u == "":
		return Other, fmt.Errorf("no datasource set: cannot tell metrics from logs")
	case u == "__expr__", strings.HasPrefix(u, "-- "):
		return Other, nil
	case strings.Contains(u, "loki"):
		return Loki, nil
	case strings.Contains(u, "logs"), strings.Contains(u, "vlogs"):
		return Logs, nil
	case strings.Contains(u, "tempo"), strings.Contains(u, "trace"):
		return Traces, nil
	case strings.Contains(u, "prom"), strings.Contains(u, "mimir"), strings.Contains(u, "metrics"),
		strings.Contains(u, "victoriametrics"), u == "vm":
		return Metrics, nil
	}
	return Other, fmt.Errorf("datasource uid %q: cannot tell metrics from logs; set model.datasource.type", dsUID)
}
