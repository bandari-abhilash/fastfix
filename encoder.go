package fastfix

import "fmt"

// Encoder for client-initiated FAST messages (Logon, MarketDataRequest,
// Logout, ...) such as those sent on a TCP recovery or request channel.
//
// It is a deliberately "lazy" encoder: for every operator-bearing field it
// sets the presence-map bit and transmits the value explicitly instead of
// exploiting copy/increment dictionaries. That is valid FAST 1.1 (an encoder
// may always transmit) and keeps decoder state consistent, at the cost of a
// few bytes per message — irrelevant for low-rate client requests, and not
// intended for encoding a high-rate market-data feed.
//
// Values map by field name:
//
//	integer kinds -> int / int32 / int64 / uint32
//	string        -> string
//	decimal       -> Decimal
//	sequence      -> []map[string]any
//
// Absent map entries encode as null (optional fields) or error (mandatory).

// Encode serializes one message for the given template, including its
// presence map and template id.
func Encode(t *Template, vals map[string]any) ([]byte, error) {
	var pb pmapBuilder
	pb.add(true) // template id is always transmitted
	var body []byte
	body, err := encodeFields(t.Fields, vals, &pb, body)
	if err != nil {
		return nil, fmt.Errorf("fast: encode %s(%d): %w", t.Name, t.ID, err)
	}
	out := pb.bytes()
	out = appendUInt(out, uint64(t.ID))
	return append(out, body...), nil
}

func encodeFields(fields []*Field, vals map[string]any, pb *pmapBuilder, out []byte) ([]byte, error) {
	for _, f := range fields {
		var err error
		switch f.Kind {
		case KindGroup:
			return nil, fmt.Errorf("group %s: encoding groups not supported", f.Name)
		case KindSequence:
			out, err = encodeSequence(f, vals, pb, out)
		default:
			out, err = encodeScalar(f, vals[f.Name], pb, out)
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func encodeSequence(f *Field, vals map[string]any, pb *pmapBuilder, out []byte) ([]byte, error) {
	v, present := vals[f.Name]
	if !present {
		if !f.Optional {
			return nil, fmt.Errorf("sequence %s: mandatory but absent", f.Name)
		}
		// null length; length fields of our client templates carry no
		// operator, so this is a plain nullable integer
		if f.Length != nil && f.Length.usesPMapBit() {
			pb.add(false)
			return out, nil
		}
		return appendUInt(out, 0), nil
	}
	elems, ok := v.([]map[string]any)
	if !ok {
		return nil, fmt.Errorf("sequence %s: want []map[string]any, got %T", f.Name, v)
	}
	n := uint64(len(elems))
	if f.Length != nil && f.Length.usesPMapBit() {
		pb.add(true)
	}
	if f.Optional {
		out = appendUInt(out, n+1)
	} else {
		out = appendUInt(out, n)
	}
	elemPMap := groupNeedsPMap(f.Fields)
	for i, elem := range elems {
		if elemPMap {
			var epb pmapBuilder
			body, err := encodeFields(f.Fields, elem, &epb, nil)
			if err != nil {
				return nil, fmt.Errorf("sequence %s[%d]: %w", f.Name, i, err)
			}
			out = append(out, epb.bytes()...)
			out = append(out, body...)
		} else {
			var err error
			out, err = encodeFields(f.Fields, elem, pb, out)
			if err != nil {
				return nil, fmt.Errorf("sequence %s[%d]: %w", f.Name, i, err)
			}
		}
	}
	return out, nil
}

func encodeScalar(f *Field, v any, pb *pmapBuilder, out []byte) ([]byte, error) {
	present := v != nil
	switch f.Operator {
	case OpNone:
		if !present {
			if !f.Optional {
				return nil, fmt.Errorf("field %s: mandatory but absent", f.Name)
			}
			return appendNull(f, out)
		}
		return appendValue(f, v, out)

	case OpConstant:
		if f.Optional {
			pb.add(present)
		} else if !present {
			// mandatory constant needs no value from the caller
			return out, nil
		}
		return out, nil // constants transmit no data

	case OpCopy, OpDefault, OpIncrement, OpTail:
		pb.add(true) // lazy: always transmit
		if !present {
			if !f.Optional {
				return nil, fmt.Errorf("field %s: mandatory but absent", f.Name)
			}
			return appendNull(f, out)
		}
		return appendValue(f, v, out)
	}
	return nil, fmt.Errorf("field %s: unsupported operator", f.Name)
}

// appendNull writes the explicit-null encoding for an optional field.
func appendNull(f *Field, out []byte) ([]byte, error) {
	switch f.Kind {
	case KindString, KindByteVector, KindUInt32, KindUInt64, KindInt32, KindInt64, KindDecimal:
		return append(out, 0x80), nil
	}
	return nil, fmt.Errorf("field %s: cannot encode null", f.Name)
}

// appendValue writes a present value, honouring nullable shifting for
// optional fields.
func appendValue(f *Field, v any, out []byte) ([]byte, error) {
	switch f.Kind {
	case KindUInt32, KindUInt64:
		n, err := toInt64(f, v)
		if err != nil {
			return nil, err
		}
		if f.Optional {
			n++
		}
		return appendUInt(out, uint64(n)), nil
	case KindInt32, KindInt64:
		n, err := toInt64(f, v)
		if err != nil {
			return nil, err
		}
		if f.Optional && n >= 0 {
			n++
		}
		return appendInt(out, n), nil
	case KindString:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("field %s: want string, got %T", f.Name, v)
		}
		return appendASCII(out, s, f.Optional), nil
	case KindDecimal:
		d, ok := v.(Decimal)
		if !ok {
			return nil, fmt.Errorf("field %s: want Decimal, got %T", f.Name, v)
		}
		exp := int64(d.Exponent)
		if f.Optional && exp >= 0 {
			exp++
		}
		out = appendInt(out, exp)
		return appendInt(out, d.Mantissa), nil
	}
	return nil, fmt.Errorf("field %s: unsupported kind", f.Name)
}

func toInt64(f *Field, v any) (int64, error) {
	switch x := v.(type) {
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case int64:
		return x, nil
	case uint32:
		return int64(x), nil
	}
	return 0, fmt.Errorf("field %s: want integer, got %T", f.Name, v)
}

// --- stop-bit primitives (inverses of reader.go) -----------------------------

func appendUInt(out []byte, v uint64) []byte {
	if v == 0 {
		return append(out, 0x80)
	}
	var tmp [10]byte
	i := len(tmp)
	stop := byte(0x80)
	for v > 0 {
		i--
		tmp[i] = byte(v&0x7f) | stop
		stop = 0
		v >>= 7
	}
	return append(out, tmp[i:]...)
}

func appendInt(out []byte, v int64) []byte {
	// collect 7-bit groups, minimal length such that the sign bit (bit 6 of
	// the first group) matches the value's sign
	var groups []byte
	x := v
	for {
		groups = append(groups, byte(x&0x7f))
		x >>= 7 // arithmetic shift keeps the sign
		last := groups[len(groups)-1]
		if (x == 0 && last&0x40 == 0) || (x == -1 && last&0x40 != 0) {
			break
		}
	}
	for i := len(groups) - 1; i >= 0; i-- {
		b := groups[i]
		if i == 0 {
			b |= 0x80
		}
		out = append(out, b)
	}
	return out
}

func appendASCII(out []byte, s string, optional bool) []byte {
	if s == "" {
		if optional {
			return append(out, 0x00, 0x80) // overlong empty = present empty
		}
		return append(out, 0x80)
	}
	for i := 0; i < len(s)-1; i++ {
		out = append(out, s[i]&0x7f)
	}
	return append(out, s[len(s)-1]|0x80)
}

// pmapBuilder accumulates presence bits (7 per byte, MSB-first) and renders
// them with the stop bit on the final byte.
type pmapBuilder struct {
	bits []bool
}

func (p *pmapBuilder) add(b bool) { p.bits = append(p.bits, b) }

func (p *pmapBuilder) bytes() []byte {
	n := (len(p.bits) + 6) / 7
	if n == 0 {
		n = 1
	}
	out := make([]byte, n)
	for i, bit := range p.bits {
		if bit {
			out[i/7] |= 0x40 >> (i % 7)
		}
	}
	out[n-1] |= 0x80
	return out
}
