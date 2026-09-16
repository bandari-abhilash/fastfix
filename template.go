package fastfix

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// Kind is the FAST field type.
type Kind int

const (
	KindUInt32 Kind = iota
	KindUInt64
	KindInt32
	KindInt64
	KindString
	KindByteVector
	KindDecimal
	KindGroup
	KindSequence
)

// Op is the field operator.
type Op int

const (
	OpNone Op = iota
	OpConstant
	OpCopy
	OpDefault
	OpDelta
	OpIncrement
	OpTail
)

// Decimal is a FAST decimal value (mantissa * 10^exponent).
type Decimal struct {
	Mantissa int64
	Exponent int32
}

// normalize strips trailing zeros from the mantissa while the exponent is
// negative, mirroring OpenFAST's DecimalValue value normalization
// ((1071500,-4).ToString() == "107.15").
func (d Decimal) normalize() (int64, int32) {
	m, e := d.Mantissa, d.Exponent
	for e < 0 && m%10 == 0 {
		m /= 10
		e++
	}
	return m, e
}

// String renders a decimal the way OpenFAST's DecimalValue.ToString() does,
// which is what downstream float/int parsing usually expects:
// (10715,-2)->"107.15", (1071500,-4)->"107.15", (5,2)->"500".
func (d Decimal) String() string {
	m, e := d.normalize()
	if e >= 0 {
		s := strconv.FormatInt(m, 10)
		if m == 0 {
			return "0"
		}
		return s + strings.Repeat("0", int(e))
	}
	neg := m < 0
	if neg {
		m = -m
	}
	digits := strconv.FormatInt(m, 10)
	scale := int(-e)
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	out := digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	if neg {
		out = "-" + out
	}
	return out
}

// Int64 mirrors OpenFAST's DecimalValue.ToLong()/ToInt(): it scales up for
// non-negative exponents, and reports failure (ok=false) for fractional
// values rather than truncating them — OpenFAST raises in that case, and
// silently rounding a price is worse than refusing it.
func (d Decimal) Int64() (int64, bool) {
	m, e := d.normalize()
	if e < 0 {
		return 0, false
	}
	return m * int64(math.Pow10(int(e))), true
}

// Field is one node of a template.
type Field struct {
	Name       string
	Kind       Kind
	Optional   bool
	Operator   Op
	HasInitial bool
	InitInt    int64
	InitDec    Decimal
	InitStr    string
	Length     *Field   // sequences: the length field
	Fields     []*Field // groups/sequences: members
}

// usesPMapBit reports whether decoding this scalar consumes a presence-map
// bit in the enclosing pmap.
func (f *Field) usesPMapBit() bool {
	switch f.Operator {
	case OpConstant:
		return f.Optional
	case OpCopy, OpDefault, OpIncrement, OpTail:
		return true
	default:
		return false
	}
}

// groupNeedsPMap reports whether a group/sequence-element needs its own pmap.
func groupNeedsPMap(fields []*Field) bool {
	for _, f := range fields {
		switch f.Kind {
		case KindGroup:
			if f.Optional {
				return true
			}
		case KindSequence:
			// the length field's bit lives in the enclosing pmap
			if f.Length != nil && f.Length.usesPMapBit() {
				return true
			}
		default:
			if f.usesPMapBit() {
				return true
			}
		}
	}
	return false
}

// Template is a message template.
type Template struct {
	ID     int
	Name   string
	Reset  bool // FAST reset template (dictionary reset)
	Fields []*Field
}

// Registry holds templates by id.
type Registry struct {
	byID map[int]*Template
}

func (r *Registry) Get(id int) *Template { return r.byID[id] }

// RegisterReset registers a FAST reset template under the given id. Feeds
// typically reference a reset template by id without declaring it in the
// template XML, so the caller supplies it programmatically — e.g.
// reg.RegisterReset(120, "FastResetTemplate").
func (r *Registry) RegisterReset(id int, name string) {
	r.byID[id] = &Template{ID: id, Name: name, Reset: true}
}

// --- XML loading ------------------------------------------------------------

type xmlNode struct {
	XMLName  xml.Name
	Name     string     `xml:"name,attr"`
	ID       string     `xml:"id,attr"`
	Presence string     `xml:"presence,attr"`
	Value    string     `xml:"value,attr"`
	Children []*xmlNode `xml:",any"`
}

// LoadTemplates parses a FAST 1.1 template definition XML.
//
// Two template-composition styles are supported:
//
//   - Flattened files, where every template inlines its own fields and
//     sequences.
//   - Reference-composed files, which build messages from shared
//     <templateRef> blocks (headers, trailers, sequence element templates)
//     and carry <typeRef> annotations.
//
// A <templateRef name="X"/> is expanded in place by inlining template X's
// fields (recursively); <typeRef> is a FIX type annotation with no bearing on
// the wire encoding and is ignored. Templates without a numeric id are
// reference-only building blocks: they are available for expansion but are
// not registered for decoding.
func LoadTemplates(rd io.Reader) (*Registry, error) {
	var root xmlNode
	dec := xml.NewDecoder(rd)
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("fast: parse templates: %w", err)
	}

	// Index every <template> by name so templateRefs can be resolved,
	// including the id-less building-block templates.
	rawByName := map[string]*xmlNode{}
	for _, tn := range root.Children {
		if tn.XMLName.Local == "template" {
			rawByName[tn.Name] = tn
		}
	}

	reg := &Registry{byID: map[int]*Template{}}
	for _, tn := range root.Children {
		if tn.XMLName.Local != "template" {
			continue
		}
		fields, err := expandChildren(tn.Children, rawByName, nil)
		if err != nil {
			return nil, fmt.Errorf("fast: template %q: %w", tn.Name, err)
		}
		if tn.ID == "" {
			continue // reference-only building block (header/trailer/element)
		}
		id, err := strconv.Atoi(tn.ID)
		if err != nil {
			return nil, fmt.Errorf("fast: template %q: bad id %q", tn.Name, tn.ID)
		}
		reg.byID[id] = &Template{ID: id, Name: tn.Name, Fields: fields}
	}
	return reg, nil
}

// expandChildren turns a node's children into a flat field list, resolving
// <templateRef> inline and dropping <typeRef>. visited guards against
// templateRef cycles.
func expandChildren(children []*xmlNode, rawByName map[string]*xmlNode, visited map[string]bool) ([]*Field, error) {
	var out []*Field
	for _, c := range children {
		switch c.XMLName.Local {
		case "typeRef":
			continue
		case "length":
			// Only meaningful inside a sequence, where buildField handles
			// it explicitly; ignore anywhere else.
			continue
		case "templateRef":
			ref, ok := rawByName[c.Name]
			if !ok {
				return nil, fmt.Errorf("unknown templateRef %q", c.Name)
			}
			if visited[c.Name] {
				return nil, fmt.Errorf("templateRef cycle at %q", c.Name)
			}
			sub, err := expandChildren(ref.Children, rawByName, withVisited(visited, c.Name))
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
		default:
			f, err := buildField(c, rawByName, visited)
			if err != nil {
				return nil, err
			}
			out = append(out, f)
		}
	}
	return out, nil
}

func withVisited(visited map[string]bool, name string) map[string]bool {
	nv := make(map[string]bool, len(visited)+1)
	for k := range visited {
		nv[k] = true
	}
	nv[name] = true
	return nv
}

var kindByElem = map[string]Kind{
	"uInt32":     KindUInt32,
	"uInt64":     KindUInt64,
	"int32":      KindInt32,
	"int64":      KindInt64,
	"string":     KindString,
	"byteVector": KindByteVector,
	"decimal":    KindDecimal,
	"group":      KindGroup,
	"sequence":   KindSequence,
}

var opByElem = map[string]Op{
	"constant":  OpConstant,
	"copy":      OpCopy,
	"default":   OpDefault,
	"delta":     OpDelta,
	"increment": OpIncrement,
	"tail":      OpTail,
}

func buildField(n *xmlNode, rawByName map[string]*xmlNode, visited map[string]bool) (*Field, error) {
	kind, ok := kindByElem[n.XMLName.Local]
	if !ok {
		return nil, fmt.Errorf("unsupported element <%s>", n.XMLName.Local)
	}
	f := &Field{Name: n.Name, Kind: kind, Optional: n.Presence == "optional"}

	switch kind {
	case KindGroup, KindSequence:
		// The length child (if any) is pulled out explicitly; every other
		// member — including <templateRef> element blocks and <typeRef>
		// annotations — is resolved by expandChildren.
		var members []*xmlNode
		for _, c := range n.Children {
			if c.XMLName.Local == "length" {
				lf := &Field{Name: c.Name, Kind: KindUInt32, Optional: f.Optional}
				if err := applyOperator(lf, c.Children); err != nil {
					return nil, err
				}
				f.Length = lf
				continue
			}
			members = append(members, c)
		}
		fields, err := expandChildren(members, rawByName, visited)
		if err != nil {
			return nil, err
		}
		f.Fields = fields
		if kind == KindSequence && f.Length == nil {
			// implicit length field with no operator
			f.Length = &Field{Name: n.Name + "__len", Kind: KindUInt32, Optional: f.Optional}
		}
	default:
		if err := applyOperator(f, n.Children); err != nil {
			return nil, err
		}
	}
	return f, nil
}

func applyOperator(f *Field, children []*xmlNode) error {
	for _, c := range children {
		op, ok := opByElem[c.XMLName.Local]
		if !ok {
			return fmt.Errorf("field %q: unsupported child <%s>", f.Name, c.XMLName.Local)
		}
		f.Operator = op
		if c.Value != "" {
			if err := setInitial(f, c.Value); err != nil {
				return err
			}
		}
	}
	if f.Operator == OpConstant && !f.HasInitial {
		return fmt.Errorf("field %q: constant without value", f.Name)
	}
	if f.Operator == OpTail && f.Kind != KindString && f.Kind != KindByteVector {
		return fmt.Errorf("field %q: tail operator only supported for string/byteVector", f.Name)
	}
	if f.Operator == OpDelta && f.Kind == KindString {
		return fmt.Errorf("field %q: string delta not supported", f.Name)
	}
	return nil
}

func setInitial(f *Field, raw string) error {
	f.HasInitial = true
	switch f.Kind {
	case KindUInt32, KindUInt64, KindInt32, KindInt64:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("field %q: bad initial value %q", f.Name, raw)
		}
		f.InitInt = v
	case KindDecimal:
		d, err := parseDecimalLiteral(raw)
		if err != nil {
			return fmt.Errorf("field %q: bad initial value %q", f.Name, raw)
		}
		f.InitDec = d
	default:
		f.InitStr = raw
	}
	return nil
}

func parseDecimalLiteral(raw string) (Decimal, error) {
	dot := strings.IndexByte(raw, '.')
	if dot < 0 {
		m, err := strconv.ParseInt(raw, 10, 64)
		return Decimal{Mantissa: m}, err
	}
	digits := raw[:dot] + raw[dot+1:]
	m, err := strconv.ParseInt(digits, 10, 64)
	return Decimal{Mantissa: m, Exponent: int32(dot - len(raw) + 1)}, err
}
