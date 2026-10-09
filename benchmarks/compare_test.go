// Package benchmarks compares fastfix with other Go FAST decoders on the same
// wire bytes. It is a separate module so that fastfix itself stays free of
// dependencies.
package benchmarks

import (
	"math"
	"strings"
	"testing"

	"github.com/bandari-abhilash/fastfix"
	fast "github.com/co11ter/goFAST"
)

// Same incremental-refresh template as fastfix's own BenchmarkDecodeIncRefresh.
const templatesXML = `<?xml version="1.0" encoding="UTF-8"?>
<templates xmlns="http://www.fixprotocol.org/ns/fast/td/1.1">
  <template name="MDIncRefresh" id="10">
    <string name="MessageType"><constant value="X"/></string>
    <uInt32 name="MsgSeqNum"><increment/></uInt32>
    <uInt64 name="SendingTime"><copy/></uInt64>
    <sequence name="MDEntries">
      <length name="NoMDEntries"/>
      <uInt32 name="MDUpdateAction"><copy/></uInt32>
      <string name="MDEntryType"><copy/></string>
      <uInt32 name="SecurityID"><copy/></uInt32>
      <uInt32 name="RptSeq"><increment/></uInt32>
      <decimal name="MDEntryPx" presence="optional"/>
      <int64 name="MDEntrySize" presence="optional"/>
      <uInt32 name="MDPriceLevel" presence="optional"><default/></uInt32>
    </sequence>
  </template>
</templates>`

const numEntries = 10

// packet encodes one incremental refresh with numEntries book entries.
func packet(tb testing.TB) []byte {
	tb.Helper()
	reg, err := fastfix.LoadTemplates(strings.NewReader(templatesXML))
	if err != nil {
		tb.Fatal(err)
	}
	entries := make([]map[string]any, numEntries)
	for i := range entries {
		side := "0"
		if i%2 == 1 {
			side = "1"
		}
		entries[i] = map[string]any{
			"MDUpdateAction": 1,
			"MDEntryType":    side,
			"SecurityID":     uint32(35001),
			"RptSeq":         uint32(1000 + i),
			"MDEntryPx":      fastfix.Decimal{Mantissa: 2245050 + int64(i)*5, Exponent: -2},
			"MDEntrySize":    int64(75 * (i + 1)),
			"MDPriceLevel":   uint32(i/2 + 1),
		}
	}
	pkt, err := fastfix.Encode(reg.Get(10), map[string]any{
		"MsgSeqNum":   uint32(123456),
		"SendingTime": int64(20261009091500123),
		"MDEntries":   entries,
	})
	if err != nil {
		tb.Fatal(err)
	}
	return pkt
}

// wantPx and wantSize are the values packet encodes for entry i.
func wantPx(i int) float64 { return float64(2245050+int64(i)*5) / 100 }
func wantSize(i int) int64 { return int64(75 * (i + 1)) }

// ---- fastfix ----

func fastfixDecoder(tb testing.TB) *fastfix.Decoder {
	tb.Helper()
	reg, err := fastfix.LoadTemplates(strings.NewReader(templatesXML))
	if err != nil {
		tb.Fatal(err)
	}
	return fastfix.NewDecoder(reg)
}

// fastfixDecode decodes one packet and returns the sum of price*size, which
// forces every value to be read.
func fastfixDecode(dec *fastfix.Decoder, pkt []byte) (float64, error) {
	dec.Ctx.Reset()
	msgs, err := dec.DecodePacket(pkt)
	if err != nil {
		return 0, err
	}
	var sum float64
	for _, e := range msgs[0].Sequence("MDEntries") {
		px, _ := e.Get("MDEntryPx").(fastfix.Decimal)
		sz, _ := e.Int("MDEntrySize")
		sum += float64(px.Mantissa) * math.Pow10(int(px.Exponent)) * float64(sz)
	}
	return sum, nil
}

// ---- goFAST ----

// loopReader serves the same packet forever. goFAST decodes from an
// io.Reader, so this is how it sees a stream of identical packets.
type loopReader struct {
	pkt []byte
	pos int
}

func (r *loopReader) Read(p []byte) (int, error) {
	n := copy(p, r.pkt[r.pos:])
	r.pos = (r.pos + n) % len(r.pkt)
	return n, nil
}

func goFASTDecoder(tb testing.TB, pkt []byte) *fast.Decoder {
	tb.Helper()
	tpls, err := fast.ParseXMLTemplate(strings.NewReader(templatesXML))
	if err != nil {
		tb.Fatal(err)
	}
	return fast.NewDecoder(&loopReader{pkt: pkt}, tpls...)
}

// reflectMsg is decoded by goFAST through reflection.
type reflectMsg struct {
	TemplateID  uint `fast:"*"`
	MessageType string
	MsgSeqNum   uint32
	SendingTime uint64
	MDEntries   []reflectEntry
}

type reflectEntry struct {
	MDUpdateAction uint32
	MDEntryType    string
	SecurityID     uint32
	RptSeq         uint32
	MDEntryPx      float64
	MDEntrySize    int64
	MDPriceLevel   uint32
}

// receiverMsg is decoded by goFAST through its Receiver interface, the
// hand-written fast path that avoids reflection.
type receiverMsg struct {
	reflectMsg
	idx int
}

func (m *receiverMsg) SetTemplateID(tid uint) { m.TemplateID = tid }

func (m *receiverMsg) SetLength(f *fast.Field) {
	if f.Name == "MDEntries" {
		n := f.Value.(int)
		if cap(m.MDEntries) < n {
			m.MDEntries = make([]reflectEntry, n)
		}
		m.MDEntries = m.MDEntries[:n]
	}
}

func (m *receiverMsg) Lock(f *fast.Field) bool {
	if f.Name != "MDEntries" {
		return false
	}
	m.idx = f.Value.(int)
	return true
}

func (m *receiverMsg) Unlock() { m.idx = 0 }

func (m *receiverMsg) SetValue(f *fast.Field) {
	if f.Value == nil {
		return
	}
	switch f.Name {
	case "MessageType":
		m.MessageType = f.Value.(string)
	case "MsgSeqNum":
		m.MsgSeqNum = f.Value.(uint32)
	case "SendingTime":
		m.SendingTime = f.Value.(uint64)
	case "MDUpdateAction":
		m.MDEntries[m.idx].MDUpdateAction = f.Value.(uint32)
	case "MDEntryType":
		m.MDEntries[m.idx].MDEntryType = f.Value.(string)
	case "SecurityID":
		m.MDEntries[m.idx].SecurityID = f.Value.(uint32)
	case "RptSeq":
		m.MDEntries[m.idx].RptSeq = f.Value.(uint32)
	case "MDEntryPx":
		m.MDEntries[m.idx].MDEntryPx = f.Value.(float64)
	case "MDEntrySize":
		m.MDEntries[m.idx].MDEntrySize = f.Value.(int64)
	case "MDPriceLevel":
		m.MDEntries[m.idx].MDPriceLevel = f.Value.(uint32)
	}
}

func sumEntries(es []reflectEntry) float64 {
	var sum float64
	for _, e := range es {
		sum += e.MDEntryPx * float64(e.MDEntrySize)
	}
	return sum
}

// ---- correctness: every decoder must agree on the packet's contents ----

func TestAllDecodersAgree(t *testing.T) {
	pkt := packet(t)

	msgs, err := fastfixDecoder(t).DecodePacket(pkt)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range msgs[0].Sequence("MDEntries") {
		px := e.Get("MDEntryPx").(fastfix.Decimal)
		sz, _ := e.Int("MDEntrySize")
		if got := float64(px.Mantissa) / 100; got != wantPx(i) || sz != wantSize(i) {
			t.Errorf("fastfix entry %d: px=%v size=%d", i, got, sz)
		}
	}

	check := func(name string, es []reflectEntry) {
		t.Helper()
		if len(es) != numEntries {
			t.Fatalf("%s: %d entries, want %d", name, len(es), numEntries)
		}
		for i, e := range es {
			if math.Abs(e.MDEntryPx-wantPx(i)) > 1e-9 || e.MDEntrySize != wantSize(i) {
				t.Errorf("%s entry %d: px=%v size=%d", name, i, e.MDEntryPx, e.MDEntrySize)
			}
		}
	}

	var rm reflectMsg
	if err := goFASTDecoder(t, pkt).Decode(&rm); err != nil {
		t.Fatal(err)
	}
	check("goFAST reflection", rm.MDEntries)

	var cm receiverMsg
	if err := goFASTDecoder(t, pkt).Decode(&cm); err != nil {
		t.Fatal(err)
	}
	check("goFAST receiver", cm.MDEntries)
}

// ---- benchmarks: reset dictionary, decode one packet, read every price and size ----

var sink float64

func BenchmarkFastfix(b *testing.B) {
	pkt := packet(b)
	dec := fastfixDecoder(b)
	b.SetBytes(int64(len(pkt)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, err := fastfixDecode(dec, pkt)
		if err != nil {
			b.Fatal(err)
		}
		sink += s
	}
}

func BenchmarkGoFASTReflection(b *testing.B) {
	pkt := packet(b)
	dec := goFASTDecoder(b, pkt)
	b.SetBytes(int64(len(pkt)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var m reflectMsg
		dec.Reset()
		if err := dec.Decode(&m); err != nil {
			b.Fatal(err)
		}
		sink += sumEntries(m.MDEntries)
	}
}

func BenchmarkGoFASTReceiver(b *testing.B) {
	pkt := packet(b)
	dec := goFASTDecoder(b, pkt)
	var m receiverMsg // reused across packets: the best case for goFAST
	b.SetBytes(int64(len(pkt)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dec.Reset()
		if err := dec.Decode(&m); err != nil {
			b.Fatal(err)
		}
		sink += sumEntries(m.MDEntries)
	}
}

// ---- multi-core: one decoder per goroutine, as with one decoder per feed ----

func BenchmarkFastfixParallel(b *testing.B) {
	pkt := packet(b)
	b.SetBytes(int64(len(pkt)))
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		dec := fastfixDecoder(b)
		var local float64
		for pb.Next() {
			s, err := fastfixDecode(dec, pkt)
			if err != nil {
				b.Error(err)
				return
			}
			local += s
		}
		_ = local
	})
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "packets/s")
}
