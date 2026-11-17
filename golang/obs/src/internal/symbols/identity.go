package symbols

import (
	"bytes"
	"debug/elf"
	"encoding/hex"
	"fmt"
	"strings"
)

// ELFBuildID reads the actual GNU note retained by split-debug ELF files.
func ELFBuildID(data []byte) (string, error) {
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("symbol file must be ELF: %w", err)
	}
	defer f.Close()
	section := f.Section(".note.gnu.build-id")
	if section == nil {
		return "", fmt.Errorf("ELF has no .note.gnu.build-id")
	}
	notes, err := section.Data()
	if err != nil {
		return "", err
	}
	for len(notes) >= 12 {
		namesz, descsz, typ := uint64(f.ByteOrder.Uint32(notes)), uint64(f.ByteOrder.Uint32(notes[4:])), f.ByteOrder.Uint32(notes[8:])
		nameEnd := 12 + namesz
		descStart := 12 + (namesz+3)&^3
		descEnd := descStart + descsz
		next := descStart + (descsz+3)&^3
		if nameEnd > uint64(len(notes)) || descEnd > uint64(len(notes)) || next > uint64(len(notes)) {
			return "", fmt.Errorf("truncated GNU build-id note")
		}
		if typ == 3 && strings.TrimRight(string(notes[12:nameEnd]), "\x00") == "GNU" && descsz > 0 {
			return hex.EncodeToString(notes[descStart:descEnd]), nil
		}
		notes = notes[next:]
	}
	return "", fmt.Errorf("GNU build-id note is absent")
}
