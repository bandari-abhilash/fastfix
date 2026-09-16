// Package fastfix implements the FAST (FIX Adapted for STreaming) 1.1 wire
// protocol in pure Go, with no dependencies outside the standard library.
//
// FAST is the binary encoding used by most exchange market-data feeds: a
// presence map plus field operators (constant, copy, default, delta,
// increment, tail) let the encoder omit anything the decoder can reconstruct
// from its dictionary, so a typical update costs a handful of bytes.
//
// # Usage
//
// Load a template definition XML into a Registry, then decode packets:
//
//	f, err := os.Open("templates.xml")
//	reg, err := fastfix.LoadTemplates(f)
//	dec := fastfix.NewDecoder(reg)
//
//	for pkt := range packets {
//		dec.Ctx.Reset() // see "Context resets" below
//		msgs, err := dec.DecodePacket(pkt)
//		for _, m := range msgs {
//			price, ok := m.Int("MDEntryPx")
//			...
//		}
//	}
//
// # Context resets
//
// This is the single most common source of wrong values. A Decoder's Context
// holds the operator dictionary that delta, copy, increment and tail
// operators build on. Whether that dictionary spans the whole stream or is
// rebuilt per packet is a property of the feed, not of FAST.
//
// UDP market-data feeds typically encode each datagram independently, so the
// caller must call Ctx.Reset() at every packet boundary. Skip it and the
// first packet decodes correctly while every later one accumulates stale
// deltas — values drift instead of failing loudly. TCP session channels
// usually do want the dictionary carried across messages; there, do not
// reset. Check the feed's specification.
//
// # Templates
//
// LoadTemplates accepts both flattened template files and reference-composed
// ones that assemble messages from shared <templateRef> blocks. Templates
// with no numeric id are treated as reference-only building blocks: available
// for inlining, never registered as decodable messages. <typeRef> is a FIX
// type annotation with no wire meaning and is ignored.
//
// Feeds commonly reference a reset template by id without declaring it in the
// XML; register it with Registry.RegisterReset.
//
// # Encoding
//
// Encode covers client-initiated session messages (Logon, MarketDataRequest,
// Logout, ...). It is deliberately lazy: every operator-bearing field sets
// its presence-map bit and transmits explicitly rather than exploiting the
// dictionary. That is valid FAST 1.1 and keeps decoder state consistent, but
// it is not meant for encoding a high-rate feed.
//
// # Scope
//
// Decoding covers scalars, groups and sequences across all six operators.
// Not implemented: encoding of groups and of the delta operator, and string
// (as opposed to byteVector) delta. These return an error rather than
// producing a wrong encoding.
package fastfix
