package fastfix

import (
	"strings"
	"testing"
)

// A market-data incremental refresh shaped like a typical exchange feed:
// header, sequence number, and a sequence of book entries.
const benchTemplates = `<?xml version="1.0" encoding="UTF-8"?>
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
  <template name="Logon" id="1">
    <string name="MessageType"/>
    <string name="SendingTime"/>
    <uInt32 name="HeartBtInt"/>
    <string name="Username"/>
    <string name="AppID" presence="optional"/>
  </template>
</templates>`

const benchEntries = 10

func benchRegistry(b *testing.B) *Registry {
	b.Helper()
	reg, err := LoadTemplates(strings.NewReader(benchTemplates))
	if err != nil {
		b.Fatal(err)
	}
	return reg
}

func benchIncRefresh(b *testing.B, reg *Registry) []byte {
	b.Helper()
	entries := make([]map[string]any, benchEntries)
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
			"MDEntryPx":      Decimal{Mantissa: 2245050 + int64(i)*5, Exponent: -2},
			"MDEntrySize":    int64(75 * (i + 1)),
			"MDPriceLevel":   uint32(i/2 + 1),
		}
	}
	pkt, err := Encode(reg.Get(10), map[string]any{
		"MsgSeqNum":   uint32(123456),
		"SendingTime": int64(20261009091500123),
		"MDEntries":   entries,
	})
	if err != nil {
		b.Fatal(err)
	}
	msgs, err := NewDecoder(reg).DecodePacket(pkt)
	if err != nil {
		b.Fatal(err)
	}
	got := msgs[0].Sequence("MDEntries")
	if len(got) != benchEntries {
		b.Fatalf("decoded %d entries, want %d", len(got), benchEntries)
	}
	last := got[benchEntries-1]
	if px := last.ValueString("MDEntryPx"); px != "22450.95" {
		b.Fatalf("last MDEntryPx = %s, want 22450.95", px)
	}
	if sz, _ := last.Int("MDEntrySize"); sz != 750 {
		b.Fatalf("last MDEntrySize = %d, want 750", sz)
	}
	return pkt
}

// One market-data packet (one message, 10 book entries), decoded the way a
// UDP feed handler does it: reset the dictionary, decode the datagram.
func BenchmarkDecodeIncRefresh(b *testing.B) {
	reg := benchRegistry(b)
	pkt := benchIncRefresh(b, reg)
	dec := NewDecoder(reg)
	b.SetBytes(int64(len(pkt)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dec.Ctx.Reset()
		if _, err := dec.DecodePacket(pkt); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.N)*benchEntries/b.Elapsed().Seconds(), "entries/s")
}

// The same packet decoded and then read field by field, which is what a
// consumer actually pays for.
func BenchmarkDecodeIncRefreshAndRead(b *testing.B) {
	reg := benchRegistry(b)
	pkt := benchIncRefresh(b, reg)
	dec := NewDecoder(reg)
	b.SetBytes(int64(len(pkt)))
	b.ReportAllocs()
	b.ResetTimer()
	var sink int64
	for i := 0; i < b.N; i++ {
		dec.Ctx.Reset()
		msgs, err := dec.DecodePacket(pkt)
		if err != nil {
			b.Fatal(err)
		}
		for _, e := range msgs[0].Sequence("MDEntries") {
			px, _ := e.Get("MDEntryPx").(Decimal)
			sz, _ := e.Int("MDEntrySize")
			sink += px.Mantissa + sz
		}
	}
	_ = sink
}

// A small session message, the Logon used by Example.
func BenchmarkDecodeLogon(b *testing.B) {
	reg := benchRegistry(b)
	pkt, err := Encode(reg.Get(1), map[string]any{
		"MessageType": "A",
		"SendingTime": "20261009-09:15:00.000000",
		"HeartBtInt":  30,
		"Username":    "USER01",
	})
	if err != nil {
		b.Fatal(err)
	}
	dec := NewDecoder(reg)
	b.SetBytes(int64(len(pkt)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := dec.DecodePacket(pkt); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodeLogon(b *testing.B) {
	reg := benchRegistry(b)
	t := reg.Get(1)
	vals := map[string]any{
		"MessageType": "A",
		"SendingTime": "20261009-09:15:00.000000",
		"HeartBtInt":  30,
		"Username":    "USER01",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Encode(t, vals); err != nil {
			b.Fatal(err)
		}
	}
}
