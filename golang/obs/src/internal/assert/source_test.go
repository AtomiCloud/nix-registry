package assert

import (
	"testing"

	"github.com/AtomiCloud/obs/internal/contract"
)

// A source: metric is stored as scraped, pod keys and all: the business-metric pod rule is not for it.
func TestSourceMetricKeepsPodKeys(t *testing.T) {
	c, err := contract.Parse("contract.yaml", []byte(`service: cluster
types:
  pod: { kind: estimate, min: 1, max: 9, churn_per_day: 0 }
metrics:
  kube_pod_info:
    source: kube-state-metrics
    kind: gauge
    budget: 9
    labels:
      pod: pod
      uid: { follows: pod }
`))
	if err != nil {
		t.Fatal(err)
	}
	if is := c.Validate(); len(is) > 0 {
		t.Fatal(is)
	}
	r := Check(c, []Series{
		sr("kube_pod_info", "pod", "a", "uid", "1", "k8s.pod.name", "a"),
		sr("kube_pod_info", "pod", "b", "uid", "2", "k8s.pod.name", "b"),
	}, Options{})
	if f := find(r, "pod-label-on-business-metric"); f != nil {
		t.Errorf("pod key on a source metric refused: %+v", f)
	}
	// the same series on an own metric stays red
	r = Check(loadC(t), []Series{
		sr("checkout_charges_total", "payment.provider", "stripe", "k8s.pod.name", "a"),
		sr("checkout_charges_total", "payment.provider", "stripe", "k8s.pod.name", "b"),
		sr("checkout_charges_total", "payment.provider", "adyen"),
	}, Options{})
	if find(r, "pod-label-on-business-metric") == nil {
		t.Errorf("own metric must stay red: %+v", r.Findings)
	}
}
