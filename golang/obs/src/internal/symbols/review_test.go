package symbols

import (
	"context"
	"encoding/binary"
	"strings"
	"testing"
)

type reviewSymbolizer struct{ offset *uint64 }

func (s reviewSymbolizer) Resolve(_ context.Context, _ []byte, off uint64) (string, error) {
	*s.offset = off
	return "realDartFunction in lib/app.dart:42", nil
}
func TestReview4RealDartStackUsesVirt(t *testing.T) {
	store := Directory{Root: t.TempDir()}
	if err := store.Put(context.Background(), "aabb.symbols", []byte("fake symbolizer fixture")); err != nil {
		t.Fatal(err)
	}
	stack := "build_id: 'aabb'\n    #00 abs 00007f1234567890 virt 0000000000123456 _kDartIsolateSnapshotInstructions+0x22\n"
	var offset uint64
	got, err := (Decoder{Store: store, Symbolizer: reviewSymbolizer{&offset}}).Decode(context.Background(), stack)
	if err != nil || offset != 0x123456 || !strings.Contains(got, "realDartFunction in lib/app.dart:42") {
		t.Fatalf("real Dart stack not decoded via virt: %x %s %v", offset, got, err)
	}
}
func reviewELF() []byte {
	b := make([]byte, 320)
	copy(b, []byte{127, 69, 76, 70, 2, 1, 1})
	u16 := func(i int, v uint16) { binary.LittleEndian.PutUint16(b[i:], v) }
	u32 := func(i int, v uint32) { binary.LittleEndian.PutUint32(b[i:], v) }
	u64 := func(i int, v uint64) { binary.LittleEndian.PutUint64(b[i:], v) }
	u16(16, 2)
	u16(18, 62)
	u32(20, 1)
	u64(40, 128)
	u16(52, 64)
	u16(58, 64)
	u16(60, 3)
	u16(62, 1)
	u32(64, 4)
	u32(68, 16)
	u32(72, 3)
	copy(b[76:], []byte("GNU\x00"))
	copy(b[80:], []byte{0, 17, 34, 51, 68, 85, 102, 119, 136, 153, 170, 187, 204, 221, 238, 255})
	names := []byte("\x00.shstrtab\x00.note.gnu.build-id\x00")
	copy(b[96:], names)
	u32(192, 1)
	u32(196, 3)
	u64(216, 96)
	u64(224, uint64(len(names)))
	u32(256, 11)
	u32(260, 7)
	u64(280, 64)
	u64(288, 32)
	u64(304, 4)
	return b
}
func TestReview6RejectMismatchedOrNonELFSymbols(t *testing.T) {
	for _, kind := range []string{"native", "dart"} {
		store := Directory{Root: t.TempDir()}
		ref := Ref{kind, "aabb", "app"}
		if _, err := Put(context.Background(), store, ref, reviewELF(), nil); err == nil {
			t.Fatalf("%s: wrong build identity accepted", kind)
		}
		if _, err := Put(context.Background(), store, ref, []byte("not ELF"), nil); err == nil {
			t.Fatalf("%s: non ELF accepted", kind)
		}
		ref.ID = "00112233445566778899aabbccddeeff"
		if _, err := Put(context.Background(), store, ref, reviewELF(), nil); err != nil {
			t.Fatal(err)
		}
	}
}
