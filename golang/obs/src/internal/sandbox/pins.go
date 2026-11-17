// Package sandbox runs the local observability stack for people (concept section
// 11.8): VictoriaMetrics, VictoriaLogs, Tempo, Pyroscope and Grafana in containers,
// loaded with the repo's dashboards and the seed. No check depends on it.
//
// Every container, volume and network it makes carries the label obs.sandbox=<project>
// and a name that starts with obs-sandbox-<project>; `down` removes only those and the
// package never runs a prune: other teams' images and containers live on the same box.
package sandbox

import (
	"fmt"
	"github.com/AtomiCloud/obs/internal/pins"
	"strings"
)

// Component is one container of the stack.
type Component struct {
	Name   string // short name: vm, vl, tempo, pyroscope, grafana
	Image  string // image repository
	Tag    string // the pin (concept section 1)
	Port   int    // port inside the container
	Offset int    // host port = port base + Offset
	Data   string // data directory mounted from a named volume
	Health string // HTTP path that answers 200 when ready
	// Declared lists every VOLUME the pinned image declares (docker image inspect .Config.Volumes).
	// Each one is mounted from a labelled named volume, so none becomes an anonymous volume.
	Declared []string
}

// Ref is the image reference the container runs.
func (c Component) Ref() string { return c.Image + ":" + c.Tag }

// Pins are the images of the stack. Each tag equals the version in concept section 1;
// nothing here is read from the environment, so the stack cannot drift from the pins.
var versions = pins.All()
var Pins = []Component{
	{Name: "grafana", Image: "grafana/grafana", Tag: versions["Grafana"].Version, Port: 3000, Offset: 0, Data: "/var/lib/grafana", Health: "/api/health"},
	{Name: "vm", Image: "victoriametrics/victoria-metrics", Tag: versions["VictoriaMetrics"].Version, Port: 8428, Offset: 1, Data: "/victoria-metrics-data", Health: "/health"},
	{Name: "vl", Image: "victoriametrics/victoria-logs", Tag: versions["VictoriaLogs"].Version, Port: 9428, Offset: 2, Data: "/victoria-logs-data", Health: "/health"},
	{Name: "tempo", Image: "grafana/tempo", Tag: versions["Tempo"].Version, Port: 3200, Offset: 3, Data: "/var/tempo", Health: "/ready"},
	{Name: "pyroscope", Image: "grafana/pyroscope", Tag: versions["Pyroscope"].Version, Port: 4040, Offset: 4, Data: "/data", Health: "/ready",
		Declared: []string{"/data", "/data-compactor", "/data-metastore"}},
}

// The Grafana VictoriaLogs datasource plugin (concept section 1).
const PluginID = "victoriametrics-logs-datasource"

var PluginVersion = strings.TrimPrefix(versions["VictoriaLogsDatasource"].Version, "v")

// ByName returns the pin of a component by short name.
func ByName(name string) (Component, bool) {
	for _, c := range Pins {
		if c.Name == name {
			return c, true
		}
	}
	return Component{}, false
}

// PinLines are the lines `obs version` prints for the sandbox images.
func PinLines() []string {
	out := make([]string, 0, len(Pins)+1)
	for _, c := range Pins {
		out = append(out, fmt.Sprintf("sandbox image %s", c.Ref()))
	}
	out = append(out, fmt.Sprintf("sandbox Grafana plugin %s %s", PluginID, "v"+PluginVersion))
	return out
}

// LaptopVLDiskCap is the VictoriaLogs disk cap on a laptop (concept section 10.4, 16 GiB).
const LaptopVLDiskCap = 16 * 1024 * 1024 * 1024
