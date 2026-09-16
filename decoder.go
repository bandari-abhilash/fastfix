package fastfix

import (
	"errors"
	"fmt"
	"strconv"
)

// Values decoded from a message:
//
//	integer kinds -> int64
//	decimal       -> Decimal
//	string        -> string
//	byteVector    -> []byte
//	group         -> *GroupVal
//	sequence      -> []*GroupVal
//
// Absent/null fields are simply not present in the map (nil).

// GroupVal holds decoded field values by name.
type GroupVal struct {
	vals map[string]any
}

func newGroupVal() *GroupVal { return &GroupVal{vals: map[string]any{}} }

// NewGroupValFrom builds a GroupVal from decoded values; used by consumer
// tests to exercise parsing logic without a wire round-trip.
func NewGroupValFrom(vals map[string]any) *GroupVal {
	g := newGroupVal()
	for k, v := range vals {
		g.vals[k] = v
	}
	return g
}

// Get returns the raw value or nil when the field is null/absent.
func (g *GroupVal) Get(name string) any { return g.vals[name] }

// Has reports whether the field holds a value.
func (g *GroupVal) Has(name string) bool { _, ok := g.vals[name]; return ok }

// ValueString renders a field as text, returning "" for a null/absent
// field rather than reporting an error.
func (g *GroupVal) ValueString(name string) string {
	v, ok := g.vals[name]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case int64:
		return strconv.FormatInt(x, 10)
	case string:
		return x
	case Decimal:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}

// Int mirrors OpenFAST's GetInt/GetLong: ok=false when the field is absent,
// and ok=false when the value is a Decimal with a fractional part (OpenFAST
// raises there rather than truncating). Callers that must not silently round
// should treat ok=false as "skip this entry".
func (g *GroupVal) Int(name string) (int64, bool) {
	switch x := g.vals[name].(type) {
	case int64:
		return x, true
	case Decimal:
		return x.Int64()
	default:
		return 0, false
	}
}

// Group returns a sub-group or nil.
func (g *GroupVal) Group(name string) *GroupVal {
	v, _ := g.vals[name].(*GroupVal)
	return v
}

// Sequence returns sequence elements or nil.
func (g *GroupVal) Sequence(name string) []*GroupVal {
	v, _ := g.vals[name].([]*GroupVal)
	return v
}

// Message is one decoded FAST message.
type Message struct {
	Template *Template
	*GroupVal
}

// Context carries decoder state across messages: the operator dictionary
// (global scope, keyed by field name, matching OpenFAST's default dictionary
// setup) and the last template id.
type Context struct {
	dict    map[string]any
	lastTID int
}

func NewContext() *Context { return &Context{dict: map[string]any{}} }

// Reset clears the dictionaries and template state.
//
// Many UDP market-data feeds encode every datagram against a fresh context
// rather than a stream-wide one. Against such a feed the caller MUST call
// Reset() at each packet boundary, otherwise delta and copy operators keep
// accumulating across packets and every value after the first is wrong.
// See TestPerPacketContextReset.
func (c *Context) Reset() {
	c.dict = map[string]any{}
	c.lastTID = 0
}

// Decoder decodes FAST messages against a template registry.
type Decoder struct {
	Reg *Registry
	Ctx *Context
	// ResetOnResetTemplate controls whether a reset template registered via
	// Registry.RegisterReset clears the dictionaries MID-packet. It defaults
	// to false: feeds commonly carry a reset template in the stream without
	// intending a dictionary reset, and per-packet reset is the caller's job
	// via Ctx.Reset().
	ResetOnResetTemplate bool
}

func NewDecoder(reg *Registry) *Decoder {
	return &Decoder{Reg: reg, Ctx: NewContext(), ResetOnResetTemplate: false}
}

// DecodeNext decodes ONE message from the front of a (possibly incomplete)
// byte stream. Returns (msg, bytesConsumed, nil) on success, (nil, 0, nil)
// when the buffer holds only a partial message (feed more bytes and retry),
// or an error for malformed data. Context state is snapshotted and restored
// on a partial decode so a retry with more bytes decodes identically.
func (d *Decoder) DecodeNext(buf []byte) (*Message, int, error) {
	if len(buf) == 0 {
		return nil, 0, nil
	}
	saveDict := make(map[string]any, len(d.Ctx.dict))
	for k, v := range d.Ctx.dict {
		saveDict[k] = v
	}
	saveTID := d.Ctx.lastTID
	r := &reader{buf: buf}
	m, err := d.decodeMessage(r)
	if err != nil {
		d.Ctx.dict = saveDict
		d.Ctx.lastTID = saveTID
		if errors.Is(err, ErrEOF) {
			return nil, 0, nil // partial message: need more bytes
		}
		return nil, 0, err
	}
	return m, r.pos, nil
}

// DecodePacket decodes every message in one UDP packet. On a malformed tail
// it returns the messages decoded so far plus the error.
func (d *Decoder) DecodePacket(buf []byte) ([]*Message, error) {
	r := &reader{buf: buf}
	var msgs []*Message
	for r.more() {
		m, err := d.decodeMessage(r)
		if err != nil {
			return msgs, err
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

func (d *Decoder) decodeMessage(r *reader) (*Message, error) {
	pm, err := r.readPMap()
	if err != nil {
		return nil, err
	}
	tid := d.Ctx.lastTID
	if pm.nextBit() {
		v, err := r.readUInt()
		if err != nil {
			return nil, err
		}
		tid = int(v)
		d.Ctx.lastTID = tid
	} else if tid == 0 {
		return nil, fmt.Errorf("fast: no template id and no previous template")
	}
	t := d.Reg.Get(tid)
	if t == nil {
		return nil, fmt.Errorf("fast: unknown template id %d", tid)
	}
	msg := &Message{Template: t, GroupVal: newGroupVal()}
	if t.Reset {
		if d.ResetOnResetTemplate {
			last := tid
			d.Ctx.Reset()
			d.Ctx.lastTID = last
		}
		return msg, nil
	}
	if err := d.decodeFields(r, pm, t.Fields, msg.GroupVal); err != nil {
		return nil, fmt.Errorf("fast: template %s(%d): %w", t.Name, tid, err)
	}
	return msg, nil
}

func (d *Decoder) decodeFields(r *reader, pm *pmap, fields []*Field, out *GroupVal) error {
	for _, f := range fields {
		switch f.Kind {
		case KindGroup:
			present := true
			if f.Optional {
				present = pm.nextBit()
			}
			if !present {
				continue
			}
			gpm := pm
			if groupNeedsPMap(f.Fields) {
				var err error
				if gpm, err = r.readPMap(); err != nil {
					return err
				}
			}
			gv := newGroupVal()
			if err := d.decodeFields(r, gpm, f.Fields, gv); err != nil {
				return fmt.Errorf("group %s: %w", f.Name, err)
			}
			out.vals[f.Name] = gv

		case KindSequence:
			v, present, err := d.decodeScalar(r, pm, f.Length)
			if err != nil {
				return fmt.Errorf("sequence %s length: %w", f.Name, err)
			}
			if !present {
				continue
			}
			n := v.(int64)
			elemPMap := groupNeedsPMap(f.Fields)
			elems := make([]*GroupVal, 0, n)
			for i := int64(0); i < n; i++ {
				epm := pm
				if elemPMap {
					if epm, err = r.readPMap(); err != nil {
						return err
					}
				}
				ev := newGroupVal()
				if err := d.decodeFields(r, epm, f.Fields, ev); err != nil {
					return fmt.Errorf("sequence %s[%d]: %w", f.Name, i, err)
				}
				elems = append(elems, ev)
			}
			out.vals[f.Name] = elems

		default:
			v, present, err := d.decodeScalar(r, pm, f)
			if err != nil {
				return fmt.Errorf("field %s: %w", f.Name, err)
			}
			if present {
				out.vals[f.Name] = v
			}
		}
	}
	return nil
}

// initialValue returns the field's initial value as a decoded value.
func initialValue(f *Field) any {
	switch f.Kind {
	case KindDecimal:
		return f.InitDec
	case KindString:
		return f.InitStr
	default:
		return f.InitInt
	}
}

// emptyValue is the zero-length base value for a tail field with no prior
// value (empty string / empty byte vector).
func emptyValue(f *Field) any {
	if f.Kind == KindByteVector {
		return []byte{}
	}
	return ""
}

// applyTail reconstructs a tail-operator value: the transmitted tail replaces
// the final characters of the previous value. When the tail is at least as
// long as the base, it replaces the whole value (FAST 1.1 tail semantics).
func applyTail(prev any, f *Field, tail any) any {
	switch f.Kind {
	case KindString:
		ts, _ := tail.(string)
		base, _ := prev.(string)
		if len(ts) >= len(base) {
			return ts
		}
		return base[:len(base)-len(ts)] + ts
	case KindByteVector:
		tb, _ := tail.([]byte)
		base, _ := prev.([]byte)
		if len(tb) >= len(base) {
			return tb
		}
		out := make([]byte, 0, len(base))
		out = append(out, base[:len(base)-len(tb)]...)
		out = append(out, tb...)
		return out
	}
	return tail
}

// decodeScalar decodes one scalar field, applying its operator against the
// dictionary. Returns (value, present, error).
func (d *Decoder) decodeScalar(r *reader, pm *pmap, f *Field) (any, bool, error) {
	key := f.Name
	switch f.Operator {
	case OpNone:
		return d.readValue(r, f)

	case OpConstant:
		if !f.Optional {
			return initialValue(f), true, nil
		}
		if pm.nextBit() {
			return initialValue(f), true, nil
		}
		return nil, false, nil

	case OpDefault:
		if pm.nextBit() {
			return d.readValue(r, f)
		}
		if f.HasInitial {
			return initialValue(f), true, nil
		}
		if f.Optional {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("mandatory default without value")

	case OpCopy:
		if pm.nextBit() {
			v, present, err := d.readValue(r, f)
			if err != nil {
				return nil, false, err
			}
			if present {
				d.Ctx.dict[key] = v
			} else {
				d.Ctx.dict[key] = nil
			}
			return v, present, nil
		}
		if prev, ok := d.Ctx.dict[key]; ok {
			if prev == nil {
				return nil, false, nil
			}
			return prev, true, nil
		}
		if f.HasInitial {
			v := initialValue(f)
			d.Ctx.dict[key] = v
			return v, true, nil
		}
		if f.Optional {
			d.Ctx.dict[key] = nil
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("mandatory copy with undefined previous value")

	case OpIncrement:
		if pm.nextBit() {
			v, present, err := d.readValue(r, f)
			if err != nil {
				return nil, false, err
			}
			// verified against OpenFAST: an explicit null does NOT
			// update the dictionary (later empty-bit fields continue
			// incrementing from the last real value)
			if present {
				d.Ctx.dict[key] = v
			}
			return v, present, nil
		}
		if prev, ok := d.Ctx.dict[key]; ok {
			if prev == nil {
				return nil, false, nil
			}
			nv := prev.(int64) + 1
			d.Ctx.dict[key] = nv
			return nv, true, nil
		}
		if f.HasInitial {
			v := initialValue(f)
			d.Ctx.dict[key] = v
			return v, true, nil
		}
		if f.Optional {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("mandatory increment with undefined previous value")

	case OpTail:
		// String/byteVector tail: when the pmap bit is set, the transmitted
		// value is the tail that replaces the end of the previous value;
		// when clear, the field keeps its previous value. Either way the
		// byte consumption matches a copy of the same field.
		if pm.nextBit() {
			v, present, err := d.readValue(r, f)
			if err != nil {
				return nil, false, err
			}
			if !present { // optional field, explicit null transmitted
				d.Ctx.dict[key] = nil
				return nil, false, nil
			}
			nv := applyTail(d.Ctx.dict[key], f, v)
			d.Ctx.dict[key] = nv
			return nv, true, nil
		}
		if prev, ok := d.Ctx.dict[key]; ok {
			if prev == nil {
				return nil, false, nil
			}
			return prev, true, nil
		}
		if f.HasInitial {
			v := initialValue(f)
			d.Ctx.dict[key] = v
			return v, true, nil
		}
		if f.Optional {
			d.Ctx.dict[key] = nil
			return nil, false, nil
		}
		// mandatory with no previous value and no initial: the base is empty
		base := emptyValue(f)
		d.Ctx.dict[key] = base
		return base, true, nil

	case OpDelta:
		if f.Kind == KindDecimal {
			return d.decodeDecimalDelta(r, f, key)
		}
		var delta int64
		if f.Optional {
			v, present, err := r.readNullableInt()
			if err != nil {
				return nil, false, err
			}
			if !present {
				return nil, false, nil
			}
			delta = v
		} else {
			v, err := r.readInt()
			if err != nil {
				return nil, false, err
			}
			delta = v
		}
		var base int64
		if prev, ok := d.Ctx.dict[key]; ok && prev != nil {
			base = prev.(int64)
		} else if f.HasInitial {
			base = f.InitInt
		}
		nv := base + delta
		d.Ctx.dict[key] = nv
		return nv, true, nil
	}
	return nil, false, fmt.Errorf("unsupported operator")
}

func (d *Decoder) decodeDecimalDelta(r *reader, f *Field, key string) (any, bool, error) {
	var expDelta int64
	if f.Optional {
		v, present, err := r.readNullableInt()
		if err != nil {
			return nil, false, err
		}
		if !present {
			return nil, false, nil
		}
		expDelta = v
	} else {
		v, err := r.readInt()
		if err != nil {
			return nil, false, err
		}
		expDelta = v
	}
	mantDelta, err := r.readInt()
	if err != nil {
		return nil, false, err
	}
	base := Decimal{}
	if prev, ok := d.Ctx.dict[key]; ok && prev != nil {
		base = prev.(Decimal)
	} else if f.HasInitial {
		base = f.InitDec
	}
	nv := Decimal{Mantissa: base.Mantissa + mantDelta, Exponent: base.Exponent + int32(expDelta)}
	d.Ctx.dict[key] = nv
	return nv, true, nil
}

// readValue reads a plain (operator-independent) value from the stream,
// honouring nullable encoding for optional fields.
func (d *Decoder) readValue(r *reader, f *Field) (any, bool, error) {
	switch f.Kind {
	case KindUInt32, KindUInt64:
		if f.Optional {
			v, present, err := r.readNullableUInt()
			return int64(v), present, err
		}
		v, err := r.readUInt()
		return int64(v), true, err
	case KindInt32, KindInt64:
		if f.Optional {
			v, present, err := r.readNullableInt()
			return v, present, err
		}
		v, err := r.readInt()
		return v, true, err
	case KindString:
		s, present, err := r.readASCII(f.Optional)
		return s, present, err
	case KindByteVector:
		b, present, err := r.readByteVector(f.Optional)
		return b, present, err
	case KindDecimal:
		var exp int64
		if f.Optional {
			v, present, err := r.readNullableInt()
			if err != nil || !present {
				return nil, false, err
			}
			exp = v
		} else {
			v, err := r.readInt()
			if err != nil {
				return nil, false, err
			}
			exp = v
		}
		mant, err := r.readInt()
		if err != nil {
			return nil, false, err
		}
		return Decimal{Mantissa: mant, Exponent: int32(exp)}, true, nil
	}
	return nil, false, fmt.Errorf("unsupported kind")
}
