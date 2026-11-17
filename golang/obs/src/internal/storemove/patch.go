package storemove

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Every change obs store move makes to an EXISTING object (the single, the compactor,
// insert/select routers, vmstorage) is a JSON patch (RFC 6902, `kubectl patch
// --type=json`) that obs builds itself from the live object. Each patch starts with
// `test` operations pinning every old value the change relies on, so the API server
// refuses the whole patch when the object drifted since it was read. Nothing an operator
// writes by hand is ever applied to an existing object.
//
// The inverse patch (also with `test` operations, pinning the values the forward patch
// wrote) is stored on the object itself in the annotation inverseAnnotation. The
// rollback never trusts it blindly: it replays the stored inverse on a copy of the live
// object, rebuilds the forward patch from the result and requires that this forward
// patch reproduces the live object exactly and yields the same inverse (verifyInverse).
//
// A self-consistent forged inverse could still name another "original" value for a field
// the forward change erases (replicas N -> 0 is many-to-one). So the forward patch also
// stores originAnnotation, the digest of the object's uid and of the pre-move value at
// every path the change writes, and the rollback requires the object its inverse
// restores to have exactly that digest. Fields obs never changes stay free (an image bump
// after the move does not block the rollback). The single's original replicas are in
// addition independently fixed: a single runs exactly 1.
//
// Every patch obs sends (forward, inverse, template and pod roles) additionally tests the
// whole live spec it was built from (sendPins), so ANY concurrent change of the object
// between obs's read and the patch (an appended flag, a duplicate -storageNode, a
// retention policy set where none was, a new container) makes the API server refuse it.

// inverseAnnotation holds the JSON inverse of the change obs made to this object.
const inverseAnnotation = "obs.diene/store-move-inverse"

// originAnnotation holds originDigest of the object before obs changed it, so a
// rollback restores exactly the values the change overwrote.
const originAnnotation = "obs.diene/store-move-origin"

// originDigest is the sha256 of the object's uid and, for every path the forward change
// fwd writes, whether it existed before and its value then (the values the inverse must
// restore). Same object, same change, same original values <=> same digest.
func originDigest(orig map[string]any, fwd []op) string {
	uid, _ := lookup(orig, "/metadata/uid")
	type entry struct {
		Path    string `json:"path"`
		Present bool   `json:"present"`
		Value   any    `json:"value"`
	}
	var cur any = deepCopy(orig)
	var entries []entry
	for _, o := range fwd {
		if o.Op != "test" {
			v, ok := lookup(cur, o.Path)
			entries = append(entries, entry{o.Path, ok, norm(v)})
		}
		var err error
		if cur, err = applyOp(cur, o); err != nil {
			return "unappliable:" + err.Error()
		}
	}
	b, _ := json.Marshal(struct {
		UID     any     `json:"uid"`
		Entries []entry `json:"entries"`
	}{uid, entries})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// sendPins are the test operations obs prepends to every patch it sends: the object's
// uid (the same object, not a re-created namesake), its whole spec and, when present, its
// whole annotations. A patch built from a read therefore applies only to exactly that
// read: any change in between (including a field added where none was, which a test of
// the field itself cannot pin) is refused by the API server, all or nothing.
func sendPins(live map[string]any) []op {
	var ops []op
	if uid, ok := lookup(live, "/metadata/uid"); ok {
		ops = append(ops, op{"test", "/metadata/uid", uid})
	}
	if spec, ok := live["spec"]; ok {
		ops = append(ops, op{"test", "/spec", norm(spec)})
	}
	if ann, ok := lookup(live, "/metadata/annotations"); ok {
		ops = append(ops, op{"test", "/metadata/annotations", norm(ann)})
	}
	return ops
}

type op struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

// MarshalJSON keeps an explicit null/zero value for test/add/replace (omitempty would
// drop a legitimate 0 or "").
func (o op) MarshalJSON() ([]byte, error) {
	if o.Op == "remove" {
		return json.Marshal(struct {
			Op   string `json:"op"`
			Path string `json:"path"`
		}{o.Op, o.Path})
	}
	return json.Marshal(struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value any    `json:"value"`
	}{o.Op, o.Path, o.Value})
}

// norm turns any value into its JSON-decoded form (int -> float64 and so on), so values
// compare the way the API server compares them.
func norm(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return v
	}
	return out
}

func escape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

func pointer(parts ...any) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteByte('/')
		switch x := p.(type) {
		case int:
			b.WriteString(strconv.Itoa(x))
		default:
			b.WriteString(escape(fmt.Sprint(x)))
		}
	}
	return b.String()
}

func splitPointer(p string) ([]string, error) {
	if p == "" {
		return nil, nil
	}
	if p[0] != '/' {
		return nil, fmt.Errorf("JSON pointer %q must start with /", p)
	}
	parts := strings.Split(p[1:], "/")
	for i, s := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
	}
	return parts, nil
}

// lookup returns the value at pointer p of doc.
func lookup(doc any, p string) (any, bool) {
	parts, err := splitPointer(p)
	if err != nil {
		return nil, false
	}
	cur := doc
	for _, k := range parts {
		switch x := cur.(type) {
		case map[string]any:
			v, ok := x[k]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(x) {
				return nil, false
			}
			cur = x[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// applyOp applies one operation to doc (a JSON-decoded tree) and returns the new root.
func applyOp(doc any, o op) (any, error) {
	parts, err := splitPointer(o.Path)
	if err != nil {
		return nil, err
	}
	if o.Op == "test" {
		v, ok := lookup(doc, o.Path)
		if !ok {
			return nil, fmt.Errorf("test %s: path absent", o.Path)
		}
		if !reflect.DeepEqual(norm(v), norm(o.Value)) {
			return nil, fmt.Errorf("test %s: live value %s, patch expects %s", o.Path, brief(v), brief(o.Value))
		}
		return doc, nil
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("%s of the whole document is not used", o.Op)
	}
	var set func(cur any, parts []string) (any, error)
	set = func(cur any, parts []string) (any, error) {
		k := parts[0]
		last := len(parts) == 1
		switch x := cur.(type) {
		case map[string]any:
			if !last {
				child, ok := x[k]
				if !ok {
					return nil, fmt.Errorf("%s %s: parent %q absent", o.Op, o.Path, k)
				}
				nc, err := set(child, parts[1:])
				if err != nil {
					return nil, err
				}
				x[k] = nc
				return x, nil
			}
			_, exists := x[k]
			switch o.Op {
			case "add":
				x[k] = norm(o.Value)
			case "replace":
				if !exists {
					return nil, fmt.Errorf("replace %s: path absent", o.Path)
				}
				x[k] = norm(o.Value)
			case "remove":
				if !exists {
					return nil, fmt.Errorf("remove %s: path absent", o.Path)
				}
				delete(x, k)
			default:
				return nil, fmt.Errorf("unsupported op %q", o.Op)
			}
			return x, nil
		case []any:
			if !last {
				i, err := strconv.Atoi(k)
				if err != nil || i < 0 || i >= len(x) {
					return nil, fmt.Errorf("%s %s: index %q out of range", o.Op, o.Path, k)
				}
				nc, err := set(x[i], parts[1:])
				if err != nil {
					return nil, err
				}
				x[i] = nc
				return x, nil
			}
			if o.Op == "add" && k == "-" {
				return append(x, norm(o.Value)), nil
			}
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i > len(x) || (o.Op != "add" && i == len(x)) {
				return nil, fmt.Errorf("%s %s: index %q out of range", o.Op, o.Path, k)
			}
			switch o.Op {
			case "add":
				out := append([]any{}, x[:i]...)
				out = append(out, norm(o.Value))
				return append(out, x[i:]...), nil
			case "replace":
				x[i] = norm(o.Value)
				return x, nil
			case "remove":
				return append(append([]any{}, x[:i]...), x[i+1:]...), nil
			}
			return nil, fmt.Errorf("unsupported op %q", o.Op)
		}
		return nil, fmt.Errorf("%s %s: parent is not an object or array", o.Op, o.Path)
	}
	return set(doc, parts)
}

// applyPatch applies ops in order to a deep copy of doc; the input is never changed.
// Like the API server it is all or nothing.
func applyPatch(doc map[string]any, ops []op) (map[string]any, error) {
	var cur any = deepCopy(doc)
	for _, o := range ops {
		var err error
		if cur, err = applyOp(cur, o); err != nil {
			return nil, err
		}
	}
	m, _ := cur.(map[string]any)
	return m, nil
}

// invert returns the operations that undo ops on doc: every mutation becomes a test of
// the value it wrote followed by the restoring mutation, in reverse order.
func invert(doc map[string]any, ops []op) ([]op, error) {
	var cur any = deepCopy(doc)
	var inv [][]op
	for _, o := range ops {
		if o.Op == "add" && strings.HasSuffix(o.Path, "/-") {
			// Appending: the undo names the concrete index the element lands at.
			if arr, ok := lookup(cur, parentOf(o.Path)); ok {
				if l, isArray := arr.([]any); isArray {
					o.Path = parentOf(o.Path) + "/" + strconv.Itoa(len(l))
				}
			}
		}
		before, existed := lookup(cur, o.Path)
		var undo []op
		switch o.Op {
		case "test":
		case "replace":
			undo = []op{{"test", o.Path, norm(o.Value)}, {"replace", o.Path, norm(before)}}
		case "remove":
			undo = []op{{"add", o.Path, norm(before)}}
		case "add":
			_, arrayParent := lookup(cur, parentOf(o.Path))
			isArray := false
			if arrayParent {
				p, _ := lookup(cur, parentOf(o.Path))
				_, isArray = p.([]any)
			}
			if existed && !isArray {
				undo = []op{{"test", o.Path, norm(o.Value)}, {"replace", o.Path, norm(before)}}
			} else {
				undo = []op{{"test", o.Path, norm(o.Value)}, {"remove", o.Path, nil}}
			}
		default:
			return nil, fmt.Errorf("unsupported op %q", o.Op)
		}
		var err error
		if cur, err = applyOp(cur, o); err != nil {
			return nil, err
		}
		inv = append(inv, undo)
	}
	var out []op
	for i := len(inv) - 1; i >= 0; i-- {
		out = append(out, inv[i]...)
	}
	return out, nil
}

func parentOf(p string) string {
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return ""
	}
	return p[:i]
}

// builder computes the forward change (tests first, then mutations, without the
// inverse annotation) from one live object. It refuses an object that is not in the
// state the change starts from.
type builder func(live map[string]any) ([]op, error)

// withInverse returns the full forward patch: the builder's operations plus the
// annotation holding their inverse (whose own last step removes the annotation).
func withInverse(live map[string]any, fwd []op) ([]op, []op, error) {
	for _, a := range []string{inverseAnnotation, originAnnotation} {
		if _, ok := lookup(live, pointer("metadata", "annotations", a)); ok {
			return nil, nil, fmt.Errorf("already carries %s (a change by an earlier run is live)", a)
		}
	}
	inv, err := invert(live, fwd)
	if err != nil {
		return nil, nil, err
	}
	inv = append(inv,
		op{"remove", pointer("metadata", "annotations", originAnnotation), nil},
		op{"remove", pointer("metadata", "annotations", inverseAnnotation), nil})
	b, err := json.Marshal(inv)
	if err != nil {
		return nil, nil, err
	}
	origin := originDigest(live, fwd)
	full := append([]op{}, fwd...)
	if _, ok := lookup(live, "/metadata/annotations"); ok {
		full = append(full,
			op{"add", pointer("metadata", "annotations", inverseAnnotation), string(b)},
			op{"add", pointer("metadata", "annotations", originAnnotation), origin})
	} else {
		full = append(full, op{"add", "/metadata/annotations", map[string]any{inverseAnnotation: string(b), originAnnotation: origin}})
	}
	return full, inv, nil
}

// storedInverse reads the inverse annotation (nil when absent).
func storedInverse(live map[string]any) ([]op, error) {
	s, ok := lookup(live, pointer("metadata", "annotations", inverseAnnotation))
	if !ok {
		return nil, nil
	}
	str, _ := s.(string)
	var ops []op
	if err := json.Unmarshal([]byte(str), &ops); err != nil || len(ops) == 0 {
		return nil, fmt.Errorf("unreadable %s annotation: %v", inverseAnnotation, err)
	}
	return ops, nil
}

// verifyInverse proves that the stored inverse undoes exactly the change build makes:
// replaying it on the live object gives the original O; build(O) must reproduce the
// live object (except the bookkeeping fields the API server changes on every write) and
// yield the same inverse. A hand-edited annotation, a different config or a live object
// that drifted afterwards is refused.
func verifyInverse(live map[string]any, stored []op, build builder) error {
	orig, err := applyPatch(live, stored)
	if err != nil {
		return fmt.Errorf("stored inverse no longer applies (live object drifted after the move): %w", err)
	}
	dropEmptyAnnotations(orig)
	fwd, err := build(orig)
	if err != nil {
		return fmt.Errorf("stored inverse restores a state the move never starts from: %w", err)
	}
	recorded, _ := lookup(live, pointer("metadata", "annotations", originAnnotation))
	if got := originDigest(orig, fwd); recorded != got {
		return fmt.Errorf("stored inverse restores values other than the ones obs recorded before the move (%s %s, restored values digest %s): hand-edited inverse or annotation, refusing", originAnnotation, brief(recorded), got)
	}
	full, inv, err := withInverse(orig, fwd)
	if err != nil {
		return err
	}
	again, err := applyPatch(orig, full)
	if err != nil {
		return fmt.Errorf("rebuilt forward patch does not apply to the restored state: %w", err)
	}
	if !reflect.DeepEqual(norm(inv), norm(stored)) {
		return fmt.Errorf("stored inverse is not the inverse obs builds for this change (hand-edited annotation or another config)")
	}
	a, b := withoutBookkeeping(again), withoutBookkeeping(live)
	if !reflect.DeepEqual(a, b) {
		return fmt.Errorf("live object differs from the moved state beyond the change obs made: %s", firstDiff(a, b))
	}
	return nil
}

// dropEmptyAnnotations mirrors the API server, which stores no empty annotations map
// (ObjectMeta.Annotations is omitempty): removing obs's only annotation leaves none.
func dropEmptyAnnotations(m map[string]any) {
	if meta, _ := m["metadata"].(map[string]any); meta != nil {
		if a, ok := meta["annotations"].(map[string]any); ok && len(a) == 0 {
			delete(meta, "annotations")
		}
	}
}

func withoutBookkeeping(m map[string]any) map[string]any {
	c, _ := norm(m).(map[string]any)
	delete(c, "status")
	if meta, _ := c["metadata"].(map[string]any); meta != nil {
		for _, k := range []string{"resourceVersion", "generation", "managedFields"} {
			delete(meta, k)
		}
	}
	return c
}

// firstDiff names the first differing path in either direction, for error messages.
func firstDiff(a, b any) string {
	var walk func(x, y any, path string) string
	walk = func(x, y any, path string) string {
		switch xs := x.(type) {
		case map[string]any:
			ys, ok := y.(map[string]any)
			if !ok {
				return path
			}
			for _, k := range sortedKeys(xs) {
				if _, ok := ys[k]; !ok {
					return path + "/" + k
				}
				if d := walk(xs[k], ys[k], path+"/"+k); d != "" {
					return d
				}
			}
			for _, k := range sortedKeys(ys) {
				if _, ok := xs[k]; !ok {
					return path + "/" + k
				}
			}
			return ""
		case []any:
			ys, ok := y.([]any)
			if !ok || len(xs) != len(ys) {
				return path
			}
			for i := range xs {
				if d := walk(xs[i], ys[i], fmt.Sprintf("%s/%d", path, i)); d != "" {
					return d
				}
			}
			return ""
		}
		if !reflect.DeepEqual(x, y) {
			return path
		}
		return ""
	}
	return walk(norm(a), norm(b), "")
}
