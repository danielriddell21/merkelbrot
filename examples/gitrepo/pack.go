package gitrepo

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Pack access follows the version 2 index format: a 256-entry fanout table over
// the first byte of each object name, then sorted names, checksums and offsets.
// Objects inside the pack may be stored whole or as a delta against another
// object, addressed either by its offset in the same pack or by its name.

const (
	idxMagic     = "\xfftOc"
	idxHeader    = 8
	idxFanout    = 256 * 4
	shaSize      = 20
	maxDeltaHops = 64
)

const (
	objCommit   = 1
	objTree     = 2
	objBlob     = 3
	objTag      = 4
	objOfsDelta = 6
	objRefDelta = 7
)

type pack struct {
	names   []byte
	offsets []uint64
	data    []byte
	count   int
}

func (s *Source) loadPacks() error {
	matches, err := filepath.Glob(filepath.Join(s.dir, "objects", "pack", "*.idx"))
	if err != nil {
		return err
	}
	sort.Strings(matches)
	for _, idxPath := range matches {
		p, err := openPack(idxPath)
		if err != nil {
			return err
		}
		s.packs = append(s.packs, p)
	}
	return nil
}

func openPack(idxPath string) (*pack, error) {
	idx, err := os.ReadFile(idxPath)
	if err != nil {
		return nil, err
	}
	if len(idx) < idxHeader+idxFanout || string(idx[:4]) != idxMagic {
		return nil, fmt.Errorf("gitrepo: %s is not a version 2 pack index", idxPath)
	}
	if v := binary.BigEndian.Uint32(idx[4:8]); v != 2 {
		return nil, fmt.Errorf("gitrepo: %s has pack index version %d, want 2", idxPath, v)
	}

	count := int(binary.BigEndian.Uint32(idx[idxHeader+idxFanout-4 : idxHeader+idxFanout]))
	namesAt := idxHeader + idxFanout
	crcAt := namesAt + count*shaSize
	offsAt := crcAt + count*4
	largeAt := offsAt + count*4
	if len(idx) < largeAt {
		return nil, fmt.Errorf("gitrepo: %s is truncated", idxPath)
	}

	offsets := make([]uint64, count)
	for i := range count {
		raw := binary.BigEndian.Uint32(idx[offsAt+i*4 : offsAt+i*4+4])
		if raw&0x8000_0000 == 0 {
			offsets[i] = uint64(raw)
			continue
		}
		// The high bit means the real offset lives in the 64-bit table.
		at := largeAt + int(raw&0x7fff_ffff)*8
		if len(idx) < at+8 {
			return nil, fmt.Errorf("gitrepo: %s has a bad large offset", idxPath)
		}
		offsets[i] = binary.BigEndian.Uint64(idx[at : at+8])
	}

	data, err := os.ReadFile(idxPath[:len(idxPath)-4] + ".pack")
	if err != nil {
		return nil, err
	}
	if len(data) < 12 || string(data[:4]) != "PACK" {
		return nil, fmt.Errorf("gitrepo: %s is not a pack file", idxPath)
	}

	return &pack{
		names:   idx[namesAt : namesAt+count*shaSize],
		offsets: offsets,
		data:    data,
		count:   count,
	}, nil
}

func (p *pack) find(sha string) (uint64, bool) {
	want, err := hex.DecodeString(sha)
	if err != nil || len(want) != shaSize {
		return 0, false
	}
	i := sort.Search(p.count, func(i int) bool {
		return bytes.Compare(p.names[i*shaSize:(i+1)*shaSize], want) >= 0
	})
	if i >= p.count || !bytes.Equal(p.names[i*shaSize:(i+1)*shaSize], want) {
		return 0, false
	}
	return p.offsets[i], true
}

func (p *pack) object(offset uint64, s *Source) (string, []byte, error) {
	return p.objectAt(offset, s, 0)
}

func (p *pack) objectAt(offset uint64, s *Source, hops int) (string, []byte, error) {
	if hops > maxDeltaHops {
		return "", nil, errors.New("gitrepo: delta chain is too long")
	}
	if offset >= uint64(len(p.data)) {
		return "", nil, errors.New("gitrepo: object offset is past the end of the pack")
	}

	at := int(offset)
	b := p.data[at]
	at++
	typ := int(b>>4) & 7
	size := uint64(b & 0x0f)
	for shift := uint(4); b&0x80 != 0; shift += 7 {
		if at >= len(p.data) {
			return "", nil, errors.New("gitrepo: truncated object header")
		}
		b = p.data[at]
		at++
		size |= uint64(b&0x7f) << shift
	}

	switch typ {
	case objCommit, objTree, objBlob, objTag:
		body, err := inflate(p.data[at:], size)
		if err != nil {
			return "", nil, err
		}
		return typeName(typ), body, nil

	case objOfsDelta:
		b = p.data[at]
		at++
		back := uint64(b & 0x7f)
		for b&0x80 != 0 {
			if at >= len(p.data) {
				return "", nil, errors.New("gitrepo: truncated delta offset")
			}
			b = p.data[at]
			at++
			back = ((back + 1) << 7) | uint64(b&0x7f)
		}
		if back > offset {
			return "", nil, errors.New("gitrepo: delta base is before the pack start")
		}
		baseType, base, err := p.objectAt(offset-back, s, hops+1)
		if err != nil {
			return "", nil, err
		}
		return applyStoredDelta(baseType, base, p.data[at:], size)

	case objRefDelta:
		if at+shaSize > len(p.data) {
			return "", nil, errors.New("gitrepo: truncated delta reference")
		}
		baseSHA := hex.EncodeToString(p.data[at : at+shaSize])
		at += shaSize
		baseType, base, err := s.object(baseSHA)
		if err != nil {
			return "", nil, err
		}
		return applyStoredDelta(baseType, base, p.data[at:], size)

	default:
		return "", nil, fmt.Errorf("gitrepo: unknown pack object type %d", typ)
	}
}

func applyStoredDelta(baseType string, base, compressed []byte, size uint64) (string, []byte, error) {
	delta, err := inflate(compressed, size)
	if err != nil {
		return "", nil, err
	}
	out, err := applyDelta(base, delta)
	if err != nil {
		return "", nil, err
	}
	return baseType, out, nil
}

func typeName(t int) string {
	switch t {
	case objCommit:
		return "commit"
	case objTree:
		return "tree"
	case objBlob:
		return "blob"
	case objTag:
		return "tag"
	default:
		return "unknown"
	}
}

func inflate(src []byte, size uint64) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("gitrepo: inflating: %w", err)
	}
	defer zr.Close()

	out := make([]byte, size)
	if _, err := io.ReadFull(zr, out); err != nil {
		return nil, fmt.Errorf("gitrepo: inflating: %w", err)
	}
	return out, nil
}

// applyDelta rebuilds an object from its base and a git delta stream, which is a
// sequence of instructions to copy a run from the base or to insert literal bytes.
func applyDelta(base, delta []byte) ([]byte, error) {
	at := 0
	readSize := func() (uint64, error) {
		var v uint64
		for shift := uint(0); ; shift += 7 {
			if at >= len(delta) {
				return 0, errors.New("gitrepo: truncated delta size")
			}
			b := delta[at]
			at++
			v |= uint64(b&0x7f) << shift
			if b&0x80 == 0 {
				return v, nil
			}
		}
	}

	srcSize, err := readSize()
	if err != nil {
		return nil, err
	}
	if srcSize != uint64(len(base)) {
		return nil, fmt.Errorf("gitrepo: delta expects a %d byte base, got %d", srcSize, len(base))
	}
	dstSize, err := readSize()
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, dstSize)
	for at < len(delta) {
		op := delta[at]
		at++
		switch {
		case op&0x80 != 0:
			var off, length uint64
			for i := range uint(4) {
				if op&(1<<i) != 0 {
					if at >= len(delta) {
						return nil, errors.New("gitrepo: truncated copy offset")
					}
					off |= uint64(delta[at]) << (8 * i)
					at++
				}
			}
			for i := range uint(3) {
				if op&(0x10<<i) != 0 {
					if at >= len(delta) {
						return nil, errors.New("gitrepo: truncated copy length")
					}
					length |= uint64(delta[at]) << (8 * i)
					at++
				}
			}
			if length == 0 {
				length = 0x10000
			}
			if off+length > uint64(len(base)) {
				return nil, errors.New("gitrepo: delta copies past the end of its base")
			}
			out = append(out, base[off:off+length]...)

		case op != 0:
			n := int(op)
			if at+n > len(delta) {
				return nil, errors.New("gitrepo: truncated delta literal")
			}
			out = append(out, delta[at:at+n]...)
			at += n

		default:
			return nil, errors.New("gitrepo: delta instruction 0 is reserved")
		}
	}
	if uint64(len(out)) != dstSize {
		return nil, fmt.Errorf("gitrepo: delta produced %d bytes, want %d", len(out), dstSize)
	}
	return out, nil
}
