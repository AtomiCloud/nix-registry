package golang

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
	"github.com/AtomiCloud/obs/internal/gen/gentest"
)

func TestGolden(t *testing.T) { gentest.Golden(t, "go") }

// The generated file compiles, and a consumer can use it the way the call surface does.
func TestCompiles(t *testing.T) {
	c, err := contract.LoadValid(gentest.Fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	fs, err := gen.Generate("go", c, gen.Options{Package: "signals"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "signals"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\n\ngo 1.24\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "signals", "signals.go"), fs[0].Content, 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

import (
	"fmt"
	"time"

	"example.com/app/signals"
)

func main() {
	shard, err := signals.NewShard(3)
	if err != nil {
		panic(err)
	}
	if _, err := signals.NewShard(16); err == nil {
		panic("16 accepted")
	}
	l := signals.CheckoutChargesLabels{Provider: signals.ProviderStripe, Outcome: signals.OutcomeOk, Shard: shard, Hour: signals.HourAt(time.Now())}
	var _ signals.DurianSignalCounter[signals.CheckoutChargesLabels] = signals.CheckoutCharges
	var _ signals.DurianSignalEvent[signals.DurianSignalLevelError] = signals.ChargeFailed
	fmt.Println(l.Provider, signals.ChargeFailed.Name(), signals.ChargeFailed.Level(), signals.MetricTable["checkout.charges"].Keys)
}
`), 0o644))
	for _, args := range [][]string{{"vet", "./..."}, {"run", "."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOCACHE="+filepath.Join(dir, ".cache"), "GOTOOLCHAIN=local")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %v: %v\n%s", args, err, out)
		}
		if args[0] == "run" && !strings.Contains(string(out), "stripe checkout.charge.failed error") {
			t.Fatalf("run output %q", out)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
