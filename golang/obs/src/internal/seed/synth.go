package seed

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	sampleEvery  = 15 * time.Second // Alloy scrape / push interval
	sampleOffset = 7 * time.Second  // samples never sit on a minute boundary
)

// Label is one name/value pair.
type Label struct{ Name, Value string }

// Series is one stored metric series.
type Series struct {
	Labels []Label // sorted, includes __name__
	Ts     []int64 // ms
	Vals   []float64
}

// Row is one stored log row (VictoriaLogs spelling).
type Row struct {
	TsNs   int64
	Fields []Label // everything except _msg and _time
	Msg    string
}

// Stream is the rows of one stream (one set of stream fields).
type Stream struct {
	Fields []Label
	Rows   []Row
}

// Dataset is everything a seed makes.
type Dataset struct {
	Start, End time.Time
	Series     map[string]*Series
	Streams    map[string]*Stream
	Traces     []*TTrace
}

// storedName is a metric or label name as the OTLP -> VictoriaMetrics path stores it.
func storedName(s string) string { return strings.ReplaceAll(s, ".", "_") }

func key(ls []Label) string {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l.Name)
		b.WriteByte(0)
		b.WriteString(l.Value)
		b.WriteByte(1)
	}
	return b.String()
}

func sorted(ls []Label) []Label {
	sort.Slice(ls, func(i, j int) bool { return ls[i].Name < ls[j].Name })
	return ls
}

func whereLabels(w map[string]string) []Label {
	var out []Label
	for k, v := range w {
		out = append(out, Label{storedName(k), v})
	}
	return sorted(out)
}

func matches(series, where map[string]string) bool {
	for k, v := range where {
		if series[k] != v {
			return false
		}
	}
	return true
}

// splitmix64 stream of uniforms, keyed by (seed, key, second).
type rnd struct{ s uint64 }

func newRnd(seed uint64, k string, sec int64) *rnd {
	h := uint64(1469598103934665603)
	for i := 0; i < len(k); i++ {
		h ^= uint64(k[i])
		h *= 1099511628211
	}
	return &rnd{s: seed*0x9E3779B97F4A7C15 ^ h ^ uint64(sec)*0xBF58476D1CE4E5B9}
}

func (r *rnd) next() uint64 {
	r.s += 0x9E3779B97F4A7C15
	z := r.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func (r *rnd) float() float64 { return float64(r.next()>>11) / (1 << 53) }

func (r *rnd) poisson(l float64) int {
	if l <= 0 {
		return 0
	}
	L := math.Exp(-l)
	k, p := 0, 1.0
	for {
		p *= r.float()
		if p <= L {
			return k
		}
		k++
	}
}

func (r *rnd) normal() float64 {
	u1, u2 := r.float(), r.float()
	if u1 < 1e-12 {
		u1 = 1e-12
	}
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

// severityNumber is the OTLP severity number for a level name.
func severityNumber(level string) string {
	switch strings.ToLower(level) {
	case "trace":
		return "1"
	case "debug":
		return "5"
	case "warn", "warning":
		return "13"
	case "error":
		return "17"
	case "fatal":
		return "21"
	}
	return "9" // info
}

// streamFieldNames are the stream fields: the bounded where-from set (concept 3.3).
var streamFieldNames = map[string]bool{
	"service.name": true, "service.namespace": true, "deployment.environment.name": true,
	"k8s.namespace.name":     true,
	"durian.cloud.landscape": true, "durian.cloud.platform": true,
	"durian.cloud.service": true, "durian.cloud.module": true,
}

// Build makes one baseline run starting at start. The run is laid out on a time
// line that ends in the past, because stores drop data dated in the future.
func Build(s *Seed, start time.Time) (*Dataset, error) {
	return BuildIncident(s, start, nil)
}

// BuildIncident uses the same deterministic stream as Build, changing only the
// matching series inside the incident's half-open window.
func BuildIncident(s *Seed, start time.Time, incident *Incident) (*Dataset, error) {
	traceDefs, err := s.traceDefs()
	if err != nil {
		return nil, err
	}
	length := s.Duration()
	ds := &Dataset{Start: start, End: start.Add(length), Series: map[string]*Series{}, Streams: map[string]*Stream{}}
	reqRate, err := ParseRate(s.Traffic.Requests)
	if err != nil {
		return nil, err
	}

	// Metric labels: the resource attributes, dots become underscores.
	var resource []Label
	for k, v := range s.Resource {
		resource = append(resource, Label{storedName(k), v})
	}
	sorted(resource)

	// Log stream fields (VictoriaLogs keeps attribute keys unchanged) and the ones Alloy adds.
	res := map[string]string{}
	for k, v := range s.Resource {
		res[k] = v
	}
	if v, ok := res["durian.cloud.service"]; ok && res["service.name"] == "" {
		res["service.name"] = v
	}
	if v, ok := res["durian.cloud.platform"]; ok && res["service.namespace"] == "" {
		res["service.namespace"] = v
	}
	if v, ok := res["durian.cloud.landscape"]; ok && res["deployment.environment.name"] == "" {
		res["deployment.environment.name"] = v
	}
	var streamFields []Label
	var resourceFields []Label // non-stream resource attributes ride on every row
	for k, v := range res {
		if streamFieldNames[k] || contains(s.StreamFields, k) {
			streamFields = append(streamFields, Label{k, v})
		} else {
			resourceFields = append(resourceFields, Label{k, v})
		}
	}
	sorted(streamFields)
	sorted(resourceFields)

	type gen struct {
		metric string
		def    MetricDef
		where  map[string]string
		share  float64
		k      string
	}
	var gens []*gen
	names := make([]string, 0, len(s.Metrics))
	for m := range s.Metrics {
		names = append(names, m)
	}
	sort.Strings(names)
	for _, m := range names {
		def := s.Metrics[m]
		for _, sr := range def.Series {
			gens = append(gens, &gen{metric: m, def: def, where: sr.Where, share: sr.Share, k: m + fmt.Sprint(whereLabels(sr.Where))})
		}
	}

	getSeries := func(ls []Label) *Series {
		ls = sorted(append([]Label{}, ls...))
		k := key(ls)
		sr := ds.Series[k]
		if sr == nil {
			sr = &Series{Labels: ls}
			ds.Series[k] = sr
		}
		return sr
	}
	getStream := func() *Stream {
		k := key(streamFields)
		st := ds.Streams[k]
		if st == nil {
			st = &Stream{Fields: streamFields}
			ds.Streams[k] = st
		}
		return st
	}

	type acc struct {
		g       *gen
		count   float64
		sum     float64
		buckets []float64
		targets []*Series
		logs    []LogDef
	}
	accs := make([]*acc, len(gens))
	for i, g := range gens {
		a := &acc{g: g, buckets: make([]float64, len(g.def.Buckets))}
		base := append(append([]Label{}, resource...), whereLabels(g.where)...)
		name := storedName(g.metric)
		switch g.def.Type {
		case "counter", "":
			a.targets = append(a.targets, getSeries(append(base, Label{"__name__", name + "_total"})))
		case "histogram":
			suffix := ""
			if g.def.Unit == "s" {
				suffix = "_seconds"
			}
			for _, le := range g.def.Buckets {
				a.targets = append(a.targets, getSeries(append(append([]Label{}, base...), Label{"__name__", name + suffix + "_bucket"}, Label{"le", fmt.Sprintf("%g", le)})))
			}
			a.targets = append(a.targets,
				getSeries(append(append([]Label{}, base...), Label{"__name__", name + suffix + "_bucket"}, Label{"le", "+Inf"})),
				getSeries(append(append([]Label{}, base...), Label{"__name__", name + suffix + "_sum"})),
				getSeries(append(append([]Label{}, base...), Label{"__name__", name + suffix + "_count"})))
		}
		for _, ld := range s.Logs {
			if ld.From.Metric == g.metric && matches(g.where, ld.From.Where) {
				a.logs = append(a.logs, ld)
			}
		}
		accs[i] = a
	}

	stream := getStream()
	startMs := start.UnixMilli()
	seconds := int64(length / time.Second)
	next := sampleOffset
	emit := func(at time.Duration) {
		ts := startMs + at.Milliseconds()
		for _, a := range accs {
			var vals []float64
			if a.g.def.Type == "histogram" {
				vals = append(append(vals, a.buckets...), a.count, a.sum, a.count)
			} else {
				vals = []float64{a.count}
			}
			for j, sr := range a.targets {
				sr.Ts = append(sr.Ts, ts)
				sr.Vals = append(sr.Vals, vals[j])
			}
		}
	}
	for sec := int64(0); sec < seconds; sec++ {
		at := time.Duration(sec) * time.Second
		for at >= next {
			emit(next)
			next += sampleEvery
		}
		shape := 1.0
		if s.Traffic.Shape == "daily_wave" {
			shape = 1 + 0.25*math.Sin(2*math.Pi*float64(sec)/86400.0)
		}
		for _, a := range accs {
			g := a.g
			r := newRnd(s.RandomSeed, g.k, sec)
			rate, latencyX := reqRate*shape*g.share, 1.0
			if incident != nil && incident.Contains(at) {
				for _, c := range incident.Change {
					if c.Metric != g.metric || !matches(g.where, c.Where) {
						continue
					}
					if c.ShareX != nil {
						rate *= *c.ShareX
					}
					if c.Rate != "" {
						extra, _ := ParseRate(c.Rate)
						rate += extra
					}
					if c.LatencyX != 0 {
						latencyX *= c.LatencyX
					}
				}
			}
			n := r.poisson(rate)
			for e := 0; e < n; e++ {
				a.count++
				frac := r.float()
				if g.def.Type == "histogram" && g.def.Latency != nil {
					v := g.def.Latency.Median * latencyX * math.Exp(g.def.Latency.Sigma*r.normal())
					a.sum += v
					for bi, le := range g.def.Buckets {
						if v <= le {
							a.buckets[bi]++
						}
					}
				}
				tsNs := start.Add(at).UnixNano() + int64(frac*1e9)
				// Separate trace randomness keeps existing metric and log seeds stable.
				tr := newRnd(s.RandomSeed, g.k+fmt.Sprint("/trace/", e), sec)
				hi, lo := tr.next(), tr.next()
				var trace *TTrace
				for _, td := range traceDefs {
					if td.From.Metric == g.metric && matches(g.where, td.From.Where) {
						trace = makeTrace(td, g.where, res, hi, lo, uint64(tsNs), tr)
						ds.Traces = append(ds.Traces, trace)
					}
				}
				for _, ld := range a.logs {
					// Log IDs use an independent stream: lazy log loading must not
					// change histogram values or timestamps of the same metric seed.
					rowRng := newRnd(s.RandomSeed, g.k+":log:"+ld.Event, sec*1_000_000+int64(e))
					row := makeRow(ld, g.where, resourceFields, tsNs, rowRng)
					if trace != nil {
						for i := range row.Fields {
							switch row.Fields[i].Name {
							case "trace_id":
								row.Fields[i].Value = fmt.Sprintf("%x", trace.TraceID)
							case "span_id":
								row.Fields[i].Value = fmt.Sprintf("%x", trace.Spans[0].SpanID)
							}
						}
					}
					stream.Rows = append(stream.Rows, row)
				}
			}
		}
	}
	for next <= length {
		emit(next)
		next += sampleEvery
	}
	for _, st := range ds.Streams {
		sort.SliceStable(st.Rows, func(i, j int) bool { return st.Rows[i].TsNs < st.Rows[j].TsNs })
	}
	return ds, nil
}

// makeRow builds one log row the way VictoriaLogs stores an OTLP log record
// (FROM SOURCE, v1.54.0 app/vlinsert/opentelemetry: attribute keys unchanged, the
// body as _msg, severity_text and severity_number, trace_id and span_id).
func makeRow(ld LogDef, where map[string]string, resource []Label, tsNs int64, r *rnd) Row {
	level := ld.Level
	if level == "" {
		level = "info"
	}
	var f []Label
	f = append(f, resource...)
	for k, v := range where {
		f = append(f, Label{k, v}) // attribute keys are kept as sent: dots stay
	}
	f = append(f, Label{"durian.cloud/event", ld.Event},
		Label{"severity_text", strings.ToUpper(level)},
		Label{"severity_number", severityNumber(level)},
		Label{"trace_id", fmt.Sprintf("%016x%016x", r.next(), r.next())},
		Label{"span_id", fmt.Sprintf("%016x", r.next())})
	msg := ld.Line
	if msg == "" {
		msg = ld.Event
	}
	return Row{TsNs: tsNs, Fields: sorted(f), Msg: msg}
}

// LabelValues returns the values of label (stored spelling) on series named metric
// ("" = any metric) that have a sample in [fromMs, toMs].
func (ds *Dataset) LabelValues(metric, label string, fromMs, toMs int64) []string {
	set := map[string]bool{}
	for _, s := range ds.Series {
		name, val, has := "", "", false
		for _, l := range s.Labels {
			if l.Name == "__name__" {
				name = l.Value
			}
			if l.Name == label {
				val, has = l.Value, true
			}
		}
		if !has || (metric != "" && name != metric) {
			continue
		}
		for _, t := range s.Ts {
			if t >= fromMs && t <= toMs {
				set[val] = true
				break
			}
		}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// MetricNames returns every stored metric name, sorted.
func (ds *Dataset) MetricNames() []string {
	set := map[string]bool{}
	for _, s := range ds.Series {
		for _, l := range s.Labels {
			if l.Name == "__name__" {
				set[l.Value] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}
