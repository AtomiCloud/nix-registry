package symbols

// Portable PDB metadata and sequence points (ECMA-335 compressed integers and
// Portable PDB v1.0 Document/MethodDebugInformation). Windows/MSF PDBs are
// explicitly refused. No address-to-IL guess is made: JIT input supplies the IL
// offset from the exporter, as required by concept section 7.1.1.
import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

type cursor struct {
	b   []byte
	at  int
	err error
}

func (c *cursor) take(n int) []byte {
	if c.err != nil {
		return nil
	}
	if n < 0 || n > len(c.b)-c.at {
		c.err = fmt.Errorf("truncated portable PDB at byte %d", c.at)
		return nil
	}
	b := c.b[c.at : c.at+n]
	c.at += n
	return b
}
func (c *cursor) u(n int) uint32 {
	b := c.take(n)
	if b == nil {
		return 0
	}
	switch n {
	case 1:
		return uint32(b[0])
	case 2:
		return uint32(binary.LittleEndian.Uint16(b))
	case 4:
		return binary.LittleEndian.Uint32(b)
	}
	c.err = fmt.Errorf("invalid integer width %d", n)
	return 0
}
func (c *cursor) compressed() (uint32, int) {
	a := c.u(1)
	if a&0x80 == 0 {
		return a, 7
	}
	if a&0xc0 == 0x80 {
		return (a&0x3f)<<8 | c.u(1), 14
	}
	if a&0xe0 == 0xc0 {
		return (a&0x1f)<<24 | c.u(1)<<16 | c.u(1)<<8 | c.u(1), 29
	}
	c.err = fmt.Errorf("invalid compressed PDB integer")
	return 0, 0
}
func (c *cursor) signed() int64 {
	u, bits := c.compressed()
	v := int64(u >> 1)
	if u&1 != 0 {
		v -= int64(1) << uint(bits-1)
	}
	return v
}

type portablePDB struct {
	docs, methods, blob            []byte
	docWidth, blobWidth, guidWidth int
	docCount, methodCount          uint32
}

func readPDB(b []byte) (portablePDB, error) {
	bad := func() (portablePDB, error) {
		return portablePDB{}, fmt.Errorf("want a Portable PDB (BSJB), not a Windows/MSF PDB")
	}
	if len(b) < 20 || !bytes.Equal(b[:4], []byte("BSJB")) {
		return bad()
	}
	c := cursor{b: b, at: 12}
	n := c.u(4)
	c.take(int(n))
	c.take(2)
	streams := c.u(2)
	parts := map[string][]byte{}
	for j := uint32(0); j < streams && c.err == nil; j++ {
		off, size := c.u(4), c.u(4)
		start := c.at
		for c.err == nil && c.u(1) != 0 {
		}
		if c.err != nil {
			break
		}
		name := string(b[start : c.at-1])
		c.take((4 - (c.at-start)%4) % 4)
		if uint64(off)+uint64(size) > uint64(len(b)) {
			return portablePDB{}, fmt.Errorf("portable PDB stream %s out of range", name)
		}
		parts[name] = b[off : off+size]
	}
	if c.err != nil {
		return portablePDB{}, c.err
	}
	tables, ok := parts["#~"]
	if !ok {
		return portablePDB{}, fmt.Errorf("portable PDB missing #~ tables")
	}
	blob, ok := parts["#Blob"]
	if !ok {
		return portablePDB{}, fmt.Errorf("portable PDB missing #Blob")
	}
	t := cursor{b: tables, at: 6}
	heap := t.u(1)
	t.take(1)
	maskBytes := t.take(8)
	t.take(8)
	if t.err != nil {
		return portablePDB{}, t.err
	}
	mask := binary.LittleEndian.Uint64(maskBytes)
	var rows [64]uint32
	for j := 0; j < 64; j++ {
		if mask&(uint64(1)<<j) != 0 {
			rows[j] = t.u(4)
			if j < 48 && rows[j] != 0 {
				return portablePDB{}, fmt.Errorf("unexpected assembly table %d in standalone Portable PDB", j)
			}
		}
	}
	if t.err != nil {
		return portablePDB{}, t.err
	}
	p := portablePDB{blob: blob, docWidth: 2, blobWidth: 2, guidWidth: 2, docCount: rows[48], methodCount: rows[49]}
	if rows[48] >= 65536 {
		p.docWidth = 4
	}
	if heap&4 != 0 {
		p.blobWidth = 4
	}
	if heap&2 != 0 {
		p.guidWidth = 4
	}
	docBytes := uint64(rows[48]) * uint64(2*p.blobWidth+2*p.guidWidth)
	methodBytes := uint64(rows[49]) * uint64(p.docWidth+p.blobWidth)
	if docBytes+methodBytes > uint64(len(t.b)-t.at) {
		return portablePDB{}, fmt.Errorf("truncated PDB document/method tables")
	}
	p.docs = t.take(int(docBytes))
	p.methods = t.take(int(methodBytes))
	return p, t.err
}
func (p portablePDB) getBlob(index uint32) ([]byte, error) {
	if uint64(index) >= uint64(len(p.blob)) {
		return nil, fmt.Errorf("PDB blob index %d out of range", index)
	}
	c := cursor{b: p.blob, at: int(index)}
	n, _ := c.compressed()
	b := c.take(int(n))
	return b, c.err
}
func (p portablePDB) document(index uint32) (string, error) {
	if index == 0 || index > p.docCount {
		return "", fmt.Errorf("PDB document %d out of range", index)
	}
	c := cursor{b: p.docs, at: int(index-1) * (2*p.blobWidth + 2*p.guidWidth)}
	name, err := p.getBlob(c.u(p.blobWidth))
	if err != nil {
		return "", err
	}
	if len(name) == 0 {
		return "", fmt.Errorf("empty PDB document name")
	}
	sep := string(name[0])
	v := cursor{b: name, at: 1}
	var segments []string
	for v.at < len(v.b) && v.err == nil {
		idx, _ := v.compressed()
		part, e := p.getBlob(idx)
		if e != nil {
			return "", e
		}
		segments = append(segments, string(part))
	}
	return strings.Join(segments, sep), v.err
}
func PDBLocation(b []byte, token, offset uint32) (string, error) {
	p, err := readPDB(b)
	if err != nil {
		return "", err
	}
	if token>>24 != 6 {
		return "", fmt.Errorf("method token 0x%08x: want MethodDef 0x06", token)
	}
	row := token & 0xffffff
	if row == 0 || row > p.methodCount {
		return "", nil
	}
	r := cursor{b: p.methods, at: int(row-1) * (p.docWidth + p.blobWidth)}
	doc := r.u(p.docWidth)
	idx := r.u(p.blobWidth)
	if idx == 0 {
		return "", nil
	}
	points, err := p.getBlob(idx)
	if err != nil {
		return "", err
	}
	c := cursor{b: points}
	c.compressed() // local signature token
	if doc == 0 {
		doc, _ = c.compressed()
	}
	var il uint64
	var line, column int64
	var best string
	first := true
	haveLocation := false
	for c.at < len(c.b) && c.err == nil {
		delta, _ := c.compressed()
		if !first && delta == 0 {
			doc, _ = c.compressed()
			continue
		}
		if first {
			il = uint64(delta)
		} else {
			il += uint64(delta)
		}
		first = false
		deltaLines, _ := c.compressed()
		var deltaCols int64
		if deltaLines == 0 {
			u, _ := c.compressed()
			deltaCols = int64(u)
		} else {
			deltaCols = c.signed()
		}
		if deltaLines == 0 && deltaCols == 0 {
			continue
		} // hidden sequence point
		if !haveLocation {
			l, _ := c.compressed()
			col, _ := c.compressed()
			line, column = int64(l), int64(col)
			haveLocation = true
		} else {
			line += c.signed()
			column += c.signed()
		}
		if line <= 0 || column < 0 {
			return "", fmt.Errorf("invalid PDB sequence point line=%d column=%d", line, column)
		}
		name, e := p.document(doc)
		if e != nil {
			return "", e
		}
		if il <= uint64(offset) {
			best = fmt.Sprintf("%s:%d", name, line)
		}
	}
	if c.err != nil {
		return "", c.err
	}
	return best, nil
}
