package check

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/files"
)

// loadContract reads <Dir>/contract.yaml when it exists. problems are contract failures that
// stop the name checks (an invalid contract names nothing reliably).
func loadContract(o *Options) (c *contract.Contract, problems []string) {
	if o.Contract != nil {
		return o.Contract, nil
	}
	if !o.hasContract() {
		return nil, nil
	}
	c, err := contract.Load(o.contractPath())
	if err != nil {
		return nil, []string{err.Error()}
	}
	if is := c.Validate(); len(is) > 0 {
		for _, i := range is {
			problems = append(problems, i.String())
		}
		return nil, problems
	}
	if o.NoOtherContracts {
		o.Contract = c
		return c, nil
	}
	// The repository's other contracts: a box dashboard reads the cluster contract's metrics.
	var others []*contract.Contract
	found, derr := contract.Discover(o.Dir)
	if derr != nil {
		problems = append(problems, derr.Error())
	}
	for _, p := range found {
		oc, err := contract.Load(p)
		if err != nil {
			problems = append(problems, strings.ReplaceAll(err.Error(), p, contract.RelPath(p)))
			continue
		}
		if is := oc.Validate(); len(is) > 0 {
			for _, i := range is {
				problems = append(problems, oc.Rel()+": "+i.String())
			}
			continue
		}
		others = append(others, oc)
	}
	for _, i := range contract.Clashes(append([]*contract.Contract{c}, others...)) {
		problems = append(problems, i.String())
	}
	if len(problems) > 0 {
		return nil, problems
	}
	if len(others) > 0 {
		c = contract.Merge(c, others)
	}
	o.Contract = c
	return c, nil
}

// readsContractNames is true when the contract is read: a contract exists.
func (o *Options) readsContractNames() bool { return o.Contract != nil }

// contractLogs checks a LogsQL query against the contract: the event filter's values against the
// contract events (concept 11.1), and each stream tag against the stream fields (concept 3.3).
func contractLogs(r *Report, q *files.Query, parsed *vl.Query, c *contract.Contract, filters []string) {
	events := c.EventNames()
	sf := c.StreamFields()
	for _, f := range filters {
		if tag, ok := strings.CutPrefix(f, "_stream:"); ok && !sf[tag] {
			r.add(Fail, q.File, q.Where, "contract-undeclared-stream-field",
				"stream field %q is not a stream field: the stream fields are the where-from set (%s)%s", tag, "durian.cloud.landscape, .platform, .service, .module, service.name, service.namespace, deployment.environment.name, k8s.namespace.name", streamHint(tag, sf))
		}
	}
	if q.IsAlert {
		return
	} // Alert event values and syntax are handled by logevent.Analyze.
	for _, ff := range parsed.FieldFilters(eventField) {
		if ff.InStream {
			continue // the event inside _stream:{} is refused by the logevent rules, not here
		}
		switch ff.Op {
		case "exact", "in", "phrase":
			for _, v := range ff.Values {
				if !has(events, v) {
					r.add(Fail, q.File, q.Where, "contract-undeclared-event",
						"event %q is not in the contract (contract/events.json: %s)%s", v, strings.Join(events, ", "), eventHint(v, events))
				}
			}
		case "prefix":
			ok := false
			for _, e := range events {
				if strings.HasPrefix(e, ff.Values[0]) {
					ok = true
				}
			}
			if !ok {
				r.add(Fail, q.File, q.Where, "contract-undeclared-event",
					"event prefix %q matches no contract event (contract/events.json: %s)%s", ff.Values[0]+"*", strings.Join(events, ", "), eventHint(ff.Values[0], events))
			}
		case "regexp":
			re, err := regexp.Compile(ff.Pattern)
			if err != nil {
				continue
			}
			match := false
			for _, e := range events {
				if re.MatchString(e) {
					match = true
				}
			}
			if !match {
				r.add(Fail, q.File, q.Where, "contract-event-regexp-no-match",
					"event regex %q matches no contract event (%s)", ff.Pattern, strings.Join(events, ", "))
			}
		default:
			r.add(Fail, q.File, q.Where, "contract-event-filter-form",
				"event filter form not checkable: write the event with :=, a quoted phrase, a prefix (name*), in(...) or :~ so its names can be checked against the contract (%s)", strings.Join(events, ", "))
		}
	}
}

func eventHint(v string, events []string) string {
	if s := contract.Suggest(v, events); s != "" {
		return fmt.Sprintf("; did you mean %q?", s)
	}
	return ""
}

func streamHint(tag string, sf map[string]bool) string {
	var all []string
	for k := range sf {
		all = append(all, k)
	}
	sort.Strings(all)
	if s := contract.Suggest(tag, all); s != "" {
		return fmt.Sprintf("; did you mean %q?", s)
	}
	return ""
}
