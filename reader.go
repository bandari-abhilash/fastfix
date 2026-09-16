package fastfix

import (
	"errors"
	"fmt"
)

// ErrEOF is returned when the packet has no further bytes.
var ErrEOF = errors.New("fast: end of packet")

// reader consumes FAST stop-bit encoded entities from a packet.
type reader struct {
	buf []byte
	pos int
}

func (r *reader) more() bool { return r.pos < len(r.buf) }

func (r *reader) next() (byte, error) {
	if r.pos >= len(r.buf) {
		return 0, ErrEOF
	}
	b := r.buf[r.pos]
	r.pos++
	return b, nil
}

// readUInt reads a stop-bit encoded unsigned integer.
func (r *reader) readUInt() (uint64, error) {
	var v uint64
	for i := 0; ; i++ {
		b, err := r.next()
		if err != nil {
			return 0, err
		}
		v = v<<7 | uint64(b&0x7f)
		if b&0x80 != 0 {
			return v, nil
		}
		if i > 9 {
			return 0, fmt.Errorf("fast: unsigned integer too long")
		}
	}
}

// readInt reads a stop-bit encoded signed integer (bit 6 of the first byte is
// the sign).
func (r *reader) readInt() (int64, error) {
	b, err := r.next()
	if err != nil {
		return 0, err
	}
	v := int64(b & 0x3f)
	if b&0x40 != 0 {
		v -= 64 // sign-extend the 7-bit first chunk
	}
	if b&0x80 != 0 {
		return v, nil
	}
	for i := 0; ; i++ {
		b, err = r.next()
		if err != nil {
			return 0, err
		}
		v = v<<7 | int64(b&0x7f)
		if b&0x80 != 0 {
			return v, nil
		}
		if i > 9 {
			return 0, fmt.Errorf("fast: signed integer too long")
		}
	}
}

// readNullableUInt reads an optional unsigned integer: 0 encodes null,
// anything else is value+1.
func (r *reader) readNullableUInt() (uint64, bool, error) {
	v, err := r.readUInt()
	if err != nil {
		return 0, false, err
	}
	if v == 0 {
		return 0, false, nil
	}
	return v - 1, true, nil
}

// readNullableInt reads an optional signed integer: 0 encodes null, positive
// values are shifted down by one.
func (r *reader) readNullableInt() (int64, bool, error) {
	v, err := r.readInt()
	if err != nil {
		return 0, false, err
	}
	if v == 0 {
		return 0, false, nil
	}
	if v > 0 {
		v--
	}
	return v, true, nil
}

// readASCII reads a stop-bit terminated ASCII string.
// Mandatory: 0x80 is the empty string. Optional: 0x80 is null and 0x00 0x80
// is the empty string.
func (r *reader) readASCII(optional bool) (string, bool, error) {
	b, err := r.next()
	if err != nil {
		return "", false, err
	}
	if b == 0x80 {
		if optional {
			return "", false, nil
		}
		return "", true, nil
	}
	if b == 0x00 {
		nb, err := r.next()
		if err != nil {
			return "", false, err
		}
		if nb == 0x80 {
			return "", true, nil // overlong empty (optional empty string)
		}
		return "", false, fmt.Errorf("fast: bad ascii encoding")
	}
	var s []byte
	for {
		s = append(s, b&0x7f)
		if b&0x80 != 0 {
			return string(s), true, nil
		}
		b, err = r.next()
		if err != nil {
			return "", false, err
		}
	}
}

// readByteVector reads a length-prefixed byte vector.
func (r *reader) readByteVector(optional bool) ([]byte, bool, error) {
	var n uint64
	if optional {
		v, present, err := r.readNullableUInt()
		if err != nil || !present {
			return nil, false, err
		}
		n = v
	} else {
		v, err := r.readUInt()
		if err != nil {
			return nil, false, err
		}
		n = v
	}
	if r.pos+int(n) > len(r.buf) {
		return nil, false, ErrEOF
	}
	out := make([]byte, n)
	copy(out, r.buf[r.pos:r.pos+int(n)])
	r.pos += int(n)
	return out, true, nil
}

// pmap is a presence map: 7 usable bits per byte, MSB-first, absent bits read
// as zero.
type pmap struct {
	bytes []byte
	idx   int
}

func (r *reader) readPMap() (*pmap, error) {
	p := &pmap{}
	for {
		b, err := r.next()
		if err != nil {
			return nil, err
		}
		p.bytes = append(p.bytes, b)
		if b&0x80 != 0 {
			return p, nil
		}
		if len(p.bytes) > 16 {
			return nil, fmt.Errorf("fast: presence map too long")
		}
	}
}

func (p *pmap) nextBit() bool {
	byteIdx := p.idx / 7
	bitIdx := p.idx % 7
	p.idx++
	if byteIdx >= len(p.bytes) {
		return false
	}
	return p.bytes[byteIdx]&(0x40>>bitIdx) != 0
}
