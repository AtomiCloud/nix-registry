package symbols

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDirectoryImmutable(t *testing.T) {
	d := Directory{Root: t.TempDir()}
	ctx := context.Background()
	if err := d.Put(ctx, "a/b", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := d.Put(ctx, "a/b", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := d.Put(ctx, "a/b", []byte("two")); err == nil {
		t.Fatal("immutable key overwritten")
	}
	if _, err := d.Get(ctx, "../secret"); err == nil {
		t.Fatal("path traversal accepted")
	}
	if _, err := d.Get(ctx, "unknown"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}
func TestKeys(t *testing.T) {
	for _, tc := range []struct {
		ref Ref
		key string
	}{{Ref{"native", "AABB", "app"}, "aabb/app.dbg"}, {Ref{"jit", "00112233-4455-6677-8899-aabbccddeeff", "app"}, "00112233-4455-6677-8899-aabbccddeeff/app.pdb"}, {Ref{"dart", "AABB", "app"}, "aabb.symbols"}} {
		got, err := tc.ref.Key()
		if err != nil || got != tc.key {
			t.Fatalf("%+v: %q %v", tc.ref, got, err)
		}
	}
	for _, ref := range []Ref{{"native", "../bad", "app"}, {"native", "aabb", "../app"}, {"native", "aab", "app"}, {"jit", "aabb", "app"}, {"bad", "aabb", "app"}} {
		if _, err := ref.Key(); err == nil {
			t.Fatalf("bad key accepted: %+v", ref)
		}
	}
}
func TestUnknownPassThrough(t *testing.T) {
	stack := "problem\n at app+0x123\nbuild-id: aabb\n at app!0x06000001+0x0 mvid:00112233-4455-6677-8899-aabbccddeeff\n"
	got, err := (Decoder{Store: Directory{Root: t.TempDir()}}).Decode(context.Background(), stack)
	if err != nil || got != stack {
		t.Fatalf("unknown changed: %q %v", got, err)
	}
}
func TestNativeELFDecode(t *testing.T) {
	if _, err := exec.LookPath("llvm-symbolizer"); err != nil {
		if os.Getenv("OBS_REQUIRE_LLVM") == "" && (errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist)) {
			t.Skip("llvm-symbolizer absent in dev shell; Nix check sets OBS_REQUIRE_LLVM=1 and requires this proof")
		}
		t.Fatal("llvm-symbolizer required for the native ELF proof (nativeCheckInputs)")
	}
	work := t.TempDir()
	src := filepath.Join(work, "fixture.go")
	source := "package main\nimport \"fmt\"\n//go:noinline\nfunc target() int { return 42 }\nfunc main() { fmt.Println(target()) }\n"
	if err := os.WriteFile(src, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(work, "fixture")
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-ldflags=-B 0x00112233445566778899aabbccddeeff", "-o", bin, src)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOWORK=off")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build offline ELF: %v: %s", err, b)
	}
	f, err := elf.Open(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	syms, err := f.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	var offset uint64
	for _, s := range syms {
		if s.Name == "main.target" {
			offset = s.Value
		}
	}
	if offset == 0 {
		t.Fatal("fixture target missing")
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	store := Directory{Root: filepath.Join(work, "store")}
	if _, err = Put(context.Background(), store, Ref{"native", "00112233445566778899aabbccddeeff", "fixture"}, data, nil); err != nil {
		t.Fatal(err)
	}
	stack := fmt.Sprintf("at fixture+0x%x\nbuild-id: 00112233445566778899aabbccddeeff\n", offset)
	got, err := (Decoder{Store: store}).Decode(context.Background(), stack)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "main.target") || !strings.Contains(got, "fixture.go:4") {
		t.Fatalf("ELF not decoded to function + file:line: %s", got)
	}
	// The same debug ELF represents Dart split debug info for the generic native
	// address decoder; real Flutter ABI artifacts are not claimed as tested.
	dartStore := Directory{Root: filepath.Join(work, "dart-store")}
	if _, err = Put(context.Background(), dartStore, Ref{"dart", "00112233445566778899aabbccddeeff", "fixture"}, data, []byte(`{"a":"realName"}`)); err != nil {
		t.Fatal(err)
	}
	dart := fmt.Sprintf("build_id: '00112233445566778899aabbccddeeff'\n#00 abs 7fffffff virt %x _kDartIsolateSnapshotInstructions+0x1\n", offset)
	got, err = (Decoder{Store: dartStore}).Decode(context.Background(), dart)
	if err != nil || !strings.Contains(got, "main.target") {
		t.Fatalf("Dart-key fallback: %s %v", got, err)
	}
}

// A tiny standards-shaped Portable PDB: Document #1 = src/Fixture.cs;
// MethodDef #1 has IL0 -> line10 and IL4 -> line12, plus a hidden IL8.
func pdbFixture() []byte {
	blob := []byte{0}
	add := func(b []byte) uint16 {
		idx := uint16(len(blob))
		blob = append(blob, byte(len(b)))
		blob = append(blob, b...)
		return idx
	}
	a := add([]byte("src"))
	b := add([]byte("Fixture.cs"))
	name := add([]byte{47, byte(a), byte(b)})
	points := add([]byte{0, 0, 0, 5, 10, 1, 4, 0, 5, 4, 0, 4, 0, 0})
	var tables bytes.Buffer
	write := func(v any) {
		if err := binary.Write(&tables, binary.LittleEndian, v); err != nil {
			panic(err)
		}
	}
	write(uint32(0))
	tables.Write([]byte{2, 0, 0, 1})
	write(uint64(1)<<48 | uint64(1)<<49)
	write(uint64(0))
	write(uint32(1))
	write(uint32(1))
	write(name)
	write(uint16(0))
	write(uint16(0))
	write(uint16(0))
	write(uint16(1))
	write(points)
	var root bytes.Buffer
	put := func(v any) {
		if err := binary.Write(&root, binary.LittleEndian, v); err != nil {
			panic(err)
		}
	}
	root.WriteString("BSJB")
	put(uint16(1))
	put(uint16(1))
	put(uint32(0))
	put(uint32(4))
	root.Write([]byte{118, 49, 0, 0})
	put(uint16(0))
	put(uint16(2))
	// 24-byte root + 12-byte #~ header + 16-byte #Blob header.
	put(uint32(52))
	put(uint32(tables.Len()))
	root.Write([]byte{35, 126, 0, 0})
	put(uint32(52 + tables.Len()))
	put(uint32(len(blob)))
	root.Write([]byte{35, 66, 108, 111, 98, 0, 0, 0})
	root.Write(tables.Bytes())
	root.Write(blob)
	return root.Bytes()
}
func TestPortablePDB(t *testing.T) {
	b := pdbFixture()
	for _, tc := range []struct {
		offset uint32
		want   string
	}{{0, "src/Fixture.cs:10"}, {3, "src/Fixture.cs:10"}, {4, "src/Fixture.cs:12"}, {8, "src/Fixture.cs:12"}} {
		got, err := PDBLocation(b, 0x06000001, tc.offset)
		if err != nil || got != tc.want {
			t.Fatalf("IL%d: %q %v", tc.offset, got, err)
		}
	}
	store := Directory{Root: t.TempDir()}
	ref := Ref{"jit", "00112233-4455-6677-8899-aabbccddeeff", "app"}
	if _, err := Put(context.Background(), store, ref, b, nil); err != nil {
		t.Fatal(err)
	}
	stack := " at app!0x06000001+0x4 mvid:" + ref.ID
	got, err := (Decoder{Store: store}).Decode(context.Background(), stack)
	if err != nil || !strings.Contains(got, "src/Fixture.cs:12") {
		t.Fatalf("%s %v", got, err)
	}
	if _, err = Put(context.Background(), store, ref, []byte("Microsoft C/C++ MSF"), nil); err == nil {
		t.Fatal("Windows PDB falsely accepted")
	}
}
func TestPDBMalformedDoesNotPanic(t *testing.T) {
	b := pdbFixture()
	for i := 0; i < len(b); i++ {
		PDBLocation(b[:i], 0x06000001, 1)
	}
	r := rand.New(rand.NewSource(42))
	for i := 0; i < 500; i++ {
		bad := append([]byte(nil), b...)
		for n := 0; n < 3; n++ {
			bad[r.Intn(len(bad))] = byte(r.Intn(256))
		}
		PDBLocation(bad, 0x06000001, 1)
	}
}

func TestMissingLLVMKeepsNativeLinesAndNotesOnce(t *testing.T) {
	ctx := context.Background()
	store := Directory{Root: t.TempDir()}
	if _, err := Put(ctx, store, Ref{"native", "00112233445566778899aabbccddeeff", "app"}, reviewELF(), nil); err != nil {
		t.Fatal(err)
	}
	stack := "at app+0x123\nat app+0x456\nbuild-id: 00112233445566778899aabbccddeeff\n"
	var notes bytes.Buffer
	got, err := (Decoder{Store: store, Symbolizer: LLVM{Executable: filepath.Join(t.TempDir(), "missing-symbolizer")}, Notes: &notes}).Decode(ctx, stack)
	if err != nil || got != stack {
		t.Fatalf("missing tool changed input or status: %q %v", got, err)
	}
	if strings.Count(notes.String(), "llvm-symbolizer") != 1 || !strings.Contains(notes.String(), "kept as is") {
		t.Fatalf("want one clear note: %q", notes.String())
	}
}
