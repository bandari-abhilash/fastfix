# fastfix

[![Go Reference](https://pkg.go.dev/badge/github.com/bandari-abhilash/fastfix.svg)](https://pkg.go.dev/github.com/bandari-abhilash/fastfix)

A pure-Go implementation of **FAST 1.1** (FIX Adapted for STreaming), the
binary encoding used by most exchange market-data feeds.

No dependencies outside the standard library. Builds on Go 1.21+.

```
go get github.com/bandari-abhilash/fastfix
```

## What it does

- **Decode** — scalars, groups and sequences under all six field operators
  (`constant`, `copy`, `default`, `delta`, `increment`, `tail`), across
  `uInt32/64`, `int32/64`, `string`, `byteVector` and `decimal`.
- **Load templates** — FAST 1.1 template XML, both flattened files and
  reference-composed ones that assemble messages from shared `<templateRef>`
  blocks.
- **Encode** — client-initiated session messages (Logon, MarketDataRequest,
  Logout, ...) for a TCP request or recovery channel.

## Quick start

```go
reg, err := fastfix.LoadTemplates(templateXML)
dec := fastfix.NewDecoder(reg)

for pkt := range packets {
    dec.Ctx.Reset()                      // see "Context resets" below
    msgs, err := dec.DecodePacket(pkt)
    for _, m := range msgs {
        px, ok := m.Int("MDEntryPx")     // ok=false if absent or fractional
        ...
    }
}
```

## Context resets — read this one

The `Context` holds the operator dictionary that `delta`, `copy`, `increment`
and `tail` build on. Whether that dictionary spans the whole stream or is
rebuilt per packet is **a property of the feed, not of FAST**.

UDP market-data feeds typically encode each datagram independently, so you
must call `dec.Ctx.Reset()` at every packet boundary. Skip it and the first
packet decodes correctly while every packet after it accumulates stale deltas
— values drift rather than failing loudly, which makes this expensive to
debug in production. TCP session channels usually *do* want the dictionary
carried across messages; there, don't reset. Check your feed's spec.

`TestPerPacketContextReset` pins this behaviour both ways.

## Decimals

`Decimal` is a mantissa/exponent pair rendered the way OpenFAST's
`DecimalValue` does, so values match what the rest of the FAST ecosystem
produces: `(10715,-2)` and `(1071500,-4)` both render as `107.15`, `(5,2)`
renders as `500`.

`GroupVal.Int` returns `ok=false` for a fractional decimal rather than
truncating it — silently rounding a price is worse than refusing it.

## Templates

- Templates with no numeric `id` are **reference-only building blocks**:
  available for `<templateRef>` inlining, never registered as decodable
  messages.
- `<typeRef>` is a FIX type annotation with no wire meaning and is dropped.
- Feeds commonly reference a reset template by id without declaring it in the
  XML. Register it yourself: `reg.RegisterReset(120, "FastResetTemplate")`.

`testdata/composed.xml` is a synthetic template set exercising every
composition feature the loader supports; it is a useful starting point if you
are writing templates by hand.

## Not implemented

These return an error rather than producing a wrong encoding:

- encoding of groups
- encoding of the `delta` operator (decoding is supported)
- `string` delta (`byteVector` delta is supported)

## License

MIT. See [LICENSE](LICENSE).
