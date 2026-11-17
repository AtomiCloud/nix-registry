package storemove

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Command is injectable: unit tests assert kubectl arguments without executing it.
type Command func(context.Context, ...string) ([]byte, error)
type Kubernetes struct {
	Command Command
	Timeout time.Duration
}

func (k Kubernetes) command(ctx context.Context, c Config, args ...string) ([]byte, error) {
	all := append([]string{"--context", c.Context, "--namespace", c.Namespace}, args...)
	if k.Command != nil {
		return k.Command(ctx, all...)
	}
	b, err := exec.CommandContext(ctx, "kubectl", all...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("kubectl %v: %w: %s", args, err, b)
	}
	return b, nil
}
func (k Kubernetes) call(ctx context.Context, c Config, args ...string) error {
	_, err := k.command(ctx, c, args...)
	return err
}
func (k Kubernetes) ready(ctx context.Context, c Config, resources []string) error {
	timeout := k.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	for _, w := range resources {
		if err := k.call(ctx, c, "rollout", "status", w, "--timeout="+timeout.String()); err != nil {
			return err
		}
		b, err := k.command(ctx, c, "get", w, "-o", "json")
		if err != nil {
			return err
		}
		var obj struct {
			Spec struct {
				Replicas *int `json:"replicas"`
			}
			Status struct {
				Ready int `json:"readyReplicas"`
			}
		}
		if err = json.Unmarshal(b, &obj); err != nil {
			return err
		}
		if obj.Spec.Replicas == nil || *obj.Spec.Replicas < 1 || obj.Status.Ready < *obj.Spec.Replicas {
			return fmt.Errorf("%s: want all nonzero desired replicas Ready", w)
		}
	}
	return nil
}
func (k Kubernetes) apply(ctx context.Context, c Config, file string) error {
	if err := validateNoServices(file); err != nil {
		return err
	}
	return k.call(ctx, c, "apply", "-f", file)
}
func (k Kubernetes) scale(ctx context.Context, c Config, w string, n int) error {
	return k.call(ctx, c, "scale", w, "--replicas="+strconv.Itoa(n))
}
func (k Kubernetes) roll(ctx context.Context, c Config, ws []string) error {
	for _, w := range ws {
		if err := k.call(ctx, c, "rollout", "restart", w); err != nil {
			return err
		}
	}
	return k.ready(ctx, c, ws)
}
func (k Kubernetes) selector(ctx context.Context, c Config, w string) (string, error) {
	b, err := k.command(ctx, c, "get", w, "-o", "json")
	if err != nil {
		return "", err
	}
	var obj struct {
		Spec struct {
			Selector struct {
				MatchLabels      map[string]string
				MatchExpressions []any
			}
		}
	}
	if err = json.Unmarshal(b, &obj); err != nil {
		return "", err
	}
	if len(obj.Spec.Selector.MatchLabels) == 0 || len(obj.Spec.Selector.MatchExpressions) > 0 {
		return "", fmt.Errorf("%s: require a nonempty matchLabels selector without expressions", w)
	}
	var parts []string
	for key, value := range obj.Spec.Selector.MatchLabels {
		for _, role := range c.RoleLabels {
			if key == role {
				return "", fmt.Errorf("%s: role %s must not occur in immutable workload selector", w, role)
			}
		}
		parts = append(parts, key+"="+value)
	}
	sort.Strings(parts)
	return strings.Join(parts, ","), nil
}
func (k Kubernetes) labels(ctx context.Context, c Config, selector string, roles []string, value string) error {
	argv := []string{"label", "pods", "-l", selector, "--overwrite"}
	for _, role := range roles {
		argv = append(argv, role+"="+value)
	}
	return k.call(ctx, c, argv...)
}

func disjointSelectors(a, b string) bool {
	values := map[string]string{}
	for _, pair := range strings.Split(a, ",") {
		key, value, _ := strings.Cut(pair, "=")
		values[key] = value
	}
	for _, pair := range strings.Split(b, ",") {
		key, value, _ := strings.Cut(pair, "=")
		if previous, ok := values[key]; ok && previous != value {
			return true
		}
	}
	return false
}
func (k Kubernetes) roles(ctx context.Context, c Config, reverse bool) error {
	// Resolve and validate every selector before changing the first label.
	singleSelector, err := k.selector(ctx, c, c.Single)
	if err != nil {
		return err
	}
	selectors := map[string]string{}
	var workloads []string
	for w := range c.RoleWorkloads {
		selector, e := k.selector(ctx, c, w)
		if e != nil {
			return e
		}
		selectors[w] = selector
		if !disjointSelectors(singleSelector, selector) {
			return fmt.Errorf("%s and %s pod selectors may overlap; require distinct component labels", c.Single, w)
		}
		workloads = append(workloads, w)
	}
	sort.Strings(workloads)
	if !reverse {
		if err = k.labels(ctx, c, singleSelector, c.RoleLabels, "false"); err != nil {
			return err
		}
	} else {
		for _, w := range workloads {
			if err = k.labels(ctx, c, selectors[w], c.RoleWorkloads[w], "false"); err != nil {
				return err
			}
		}
	}
	targets := workloads
	if reverse {
		targets = []string{c.Single}
	}
	for _, w := range targets {
		roles := c.RoleWorkloads[w]
		selector := selectors[w]
		if reverse {
			roles = c.RoleLabels
			selector = singleSelector
		}
		if err = k.labels(ctx, c, selector, roles, "true"); err != nil {
			return err
		}
		labels := map[string]string{}
		for _, role := range c.RoleLabels {
			labels[role] = "false"
		}
		for _, role := range roles {
			labels[role] = "true"
		}
		patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"labels": labels}}}})
		// Never roll the old single before its drain; persist its false template
		// only after scale-to-zero. New components receive only their assigned roles.
		if err = k.call(ctx, c, "patch", w, "--type=merge", "-p", string(patch)); err != nil {
			return err
		}
	}
	return k.ready(ctx, c, targets)
}
func (k Kubernetes) compaction(ctx context.Context, c Config, resume bool) error {
	if c.Compactor != "" {
		n := 0
		if resume {
			n = c.CompactorReplicas
		}
		return k.scale(ctx, c, c.Compactor, n)
	}
	args := c.StopCompaction
	if resume {
		args = c.ResumeCompaction
	}
	if len(args) == 0 {
		return fmt.Errorf("Tempo compaction control argv missing")
	}
	argv := append([]string{"exec", c.Single, "--"}, args...)
	return k.call(ctx, c, argv...)
}
func (k Kubernetes) Execute(ctx context.Context, s Step, store string, c Config) error {
	switch s {
	case Prepare, AddNodes:
		if err := validateScaledManifest(c); err != nil {
			return err
		}
		if err := k.apply(ctx, c, c.ScaledManifest); err != nil {
			return err
		}
		return k.ready(ctx, c, c.Scaled)
	case Route:
		return k.roles(ctx, c, false)
	case Reconnect:
		b, err := k.command(ctx, c, "get", c.Alloy, "-o", "jsonpath={.spec.replicas}")
		if err != nil {
			return err
		}
		n, err := strconv.Atoi(string(b))
		if err != nil || n < 2 {
			return fmt.Errorf("%s: Alloy must have at least 2 replicas, got %q", c.Alloy, b)
		}
		return k.roll(ctx, c, []string{c.Alloy})
	case StorageRoute, RestoreRoute:
		file := c.RoutingManifest
		if s == RestoreRoute {
			file = c.OriginalRoutingManifest
		}
		if err := k.apply(ctx, c, file); err != nil {
			return err
		}
		return k.roll(ctx, c, c.Routers)
	case Step("stop compaction"):
		return k.compaction(ctx, c, false)
	case Step("retain single as metastore"):
		if err := k.apply(ctx, c, c.MetastoreManifest); err != nil {
			return err
		}
		return k.ready(ctx, c, []string{c.Single})
	case Step("stop single"):
		if err := k.scale(ctx, c, c.Single, 0); err != nil {
			return err
		}
		labels := map[string]string{}
		for _, label := range c.RoleLabels {
			labels[label] = "false"
		}
		patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"labels": labels}}}})
		return k.call(ctx, c, "patch", c.Single, "--type=merge", "-p", string(patch))
	case Restore:
		if err := k.apply(ctx, c, c.SingleManifest); err != nil {
			return err
		}
		if err := k.scale(ctx, c, c.Single, 1); err != nil {
			return err
		}
		if err := k.ready(ctx, c, []string{c.Single}); err != nil {
			return err
		}
		if store == "tempo" {
			if err := k.compaction(ctx, c, true); err != nil {
				return err
			}
		}
		if err := k.roles(ctx, c, true); err != nil {
			return err
		}
		// The scaled form is retained. In particular, no new Pyroscope metastore
		// PVC or Kafka topic is removed during rollback.
		return k.Execute(ctx, Reconnect, store, c)
	default:
		return fmt.Errorf("unsupported cluster step %q", s)
	}
}
