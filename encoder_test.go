package fastfix

import "testing"

// TestEncodeStopBitPrimitives pins the stop-bit encodings against the reader.
func TestEncodeStopBitPrimitives(t *testing.T) {
	uints := []uint64{0, 1, 5, 127, 128, 300, 16384, 942755}
	for _, v := range uints {
		buf := appendUInt(nil, v)
		r := &reader{buf: buf}
		got, err := r.readUInt()
		if err != nil || got != v {
			t.Errorf("uint %d: round-trip got %d, err %v (bytes %x)", v, got, err, buf)
		}
	}
	ints := []int64{0, 1, -1, 63, 64, -63, -64, -65, 300, -300, 8193}
	for _, v := range ints {
		buf := appendInt(nil, v)
		r := &reader{buf: buf}
		got, err := r.readInt()
		if err != nil || got != v {
			t.Errorf("int %d: round-trip got %d, err %v (bytes %x)", v, got, err, buf)
		}
	}
	strs := []string{"A", "V", "hello", "20260716-09:15:00.000000", "FU"}
	for _, s := range strs {
		buf := appendASCII(nil, s, false)
		r := &reader{buf: buf}
		got, _, err := r.readASCII(false)
		if err != nil || got != s {
			t.Errorf("ascii %q: round-trip got %q, err %v", s, got, err)
		}
		buf = appendASCII(nil, s, true)
		r = &reader{buf: buf}
		got, present, err := r.readASCII(true)
		if err != nil || !present || got != s {
			t.Errorf("optional ascii %q: round-trip got %q present=%v err %v", s, got, present, err)
		}
	}
}

// TestEncodeLogonRoundTrip encodes a session-level message and decodes it
// back, covering the tail operator and absent optional fields.
func TestEncodeLogonRoundTrip(t *testing.T) {
	reg := openTemplates(t)
	logon := reg.Get(1)
	if logon == nil {
		t.Fatal("template 1 (Logon) not registered")
	}
	vals := map[string]any{
		"MessageType": "A",
		"SendingTime": "20260716-09:15:00.000000",
		"HeartBtInt":  10,
		"Username":    "USER01",
		"Password":    "secret",
	}
	buf, err := Encode(logon, vals)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	msgs, err := NewDecoder(reg).DecodePacket(buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Template.ID != 1 {
		t.Fatalf("decoded %d messages, first template %v", len(msgs), msgs[0].Template)
	}
	m := msgs[0]
	for k, want := range map[string]string{
		"MessageType": "A", "SendingTime": "20260716-09:15:00.000000",
		"Username": "USER01", "Password": "secret", "HeartBtInt": "10",
	} {
		if got := m.ValueString(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if m.Has("AppID") || m.Has("SessionStatus") {
		t.Error("absent optional fields decoded as present")
	}
}

// TestEncodeDataRequestRoundTrip covers sequences — including an element
// field carrying a copy operator, which forces per-element presence maps.
func TestEncodeDataRequestRoundTrip(t *testing.T) {
	reg := openTemplates(t)
	req := reg.Get(7)
	if req == nil {
		t.Fatal("template 7 (DataRequest) not registered")
	}
	vals := map[string]any{
		"MessageType":      "V",
		"SendingTime":      "20260716-09:15:00.000000",
		"ReqID":            "1",
		"SubscriptionType": 0,
		"NoEntryTypes": []map[string]any{
			{"EntryType": "0"},
			{"EntryType": "1"},
			{"EntryType": "7"},
			{"EntryType": "2", "EntryTime": "00:00:00.000"},
		},
		"RelatedSymbol": []map[string]any{
			{"ProductComplex": "FU"},
			{"ProductComplex": "OP"},
		},
	}
	buf, err := Encode(req, vals)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	msgs, err := NewDecoder(reg).DecodePacket(buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Template.ID != 7 {
		t.Fatalf("decoded %d messages", len(msgs))
	}
	m := msgs[0]
	if got := m.ValueString("ReqID"); got != "1" {
		t.Errorf("ReqID = %q", got)
	}
	if got := m.ValueString("SubscriptionType"); got != "0" {
		t.Errorf("SubscriptionType = %q", got)
	}
	ets := m.Sequence("NoEntryTypes")
	if len(ets) != 4 {
		t.Fatalf("NoEntryTypes len = %d, want 4", len(ets))
	}
	for i, want := range []string{"0", "1", "7", "2"} {
		if got := ets[i].ValueString("EntryType"); got != want {
			t.Errorf("entry %d EntryType = %q, want %q", i, got, want)
		}
	}
	if got := ets[3].ValueString("EntryTime"); got != "00:00:00.000" {
		t.Errorf("entry 3 EntryTime = %q", got)
	}
	syms := m.Sequence("RelatedSymbol")
	if len(syms) != 2 {
		t.Fatalf("RelatedSymbol len = %d, want 2", len(syms))
	}
	if a, b := syms[0].ValueString("ProductComplex"), syms[1].ValueString("ProductComplex"); a != "FU" || b != "OP" {
		t.Errorf("segments = %q %q", a, b)
	}
}
