package fastfix

import "testing"

// Hand-built template: uInt32 "V" (copy) + int64 "D" (delta), template id 200.
func miniRegistry() *Registry {
	return &Registry{byID: map[int]*Template{
		200: {ID: 200, Name: "T", Fields: []*Field{
			{Name: "V", Kind: KindUInt32, Operator: OpCopy},
			{Name: "D", Kind: KindInt64, Operator: OpDelta},
		}},
	}}
}

// Hand-encoded message for template 200 with V=5, D(delta)=+10:
// pmap 0xE0 (tid bit + V bit, stop), tid 200 (0x01 0xC8), V 0x85, D 0x8A.
var miniMsg = []byte{0xE0, 0x01, 0xC8, 0x85, 0x8A}

// Feeds that encode every UDP datagram against a fresh context require the
// decoder context to be reset at each packet boundary: two identical packets
// must decode to identical values. Without the reset the delta base carries
// over and every packet after the first decodes wrong.
func TestPerPacketContextReset(t *testing.T) {
	d := NewDecoder(miniRegistry())

	decode := func() (v, delta int64) {
		t.Helper()
		msgs, err := d.DecodePacket(miniMsg)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(msgs) != 1 {
			t.Fatalf("decoded %d messages", len(msgs))
		}
		v, _ = msgs[0].Int("V")
		delta, _ = msgs[0].Int("D")
		return
	}

	v1, d1 := decode()
	if v1 != 5 || d1 != 10 {
		t.Fatalf("packet 1: V=%d D=%d, want 5/10", v1, d1)
	}

	// WITHOUT reset the delta accumulates (10+10=20) — the live-feed bug.
	v2, d2 := decode()
	if d2 != 20 {
		t.Fatalf("sanity: without reset D=%d, want 20 (accumulated)", d2)
	}
	_ = v2

	// The consumer resets per packet, which must re-base the delta.
	d.Ctx.Reset()
	v3, d3 := decode()
	if v3 != 5 || d3 != 10 {
		t.Fatalf("after Reset: V=%d D=%d, want 5/10", v3, d3)
	}
}
