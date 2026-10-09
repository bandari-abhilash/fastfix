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

## Performance

Apple M5 (4 performance + 6 efficiency cores), Go 1.26, median of 6 runs.
The workload is one 174-byte market-data packet: an incremental refresh with
10 book entries using `constant`, `increment`, `copy` and `default`
operators, optional decimals and a sequence. Each packet is decoded after a
dictionary reset, as a UDP feed handler would, and every entry's price and
size is read back.

### Compared with goFAST

[goFAST](https://github.com/co11ter/goFAST) is the other pure-Go FAST
library. Both decode the identical bytes, and a test checks that they agree
on every value. Single core:

```
                                  time/packet    packets/s    allocs/packet
fastfix                               2.5 µs        400k           111
goFAST, hand-written Receiver         2.9 µs        340k            65
goFAST, reflection                    5.2 µs        190k           146
```

fastfix is 1.2× faster than goFAST's hand-written `Receiver` path, which
needs a per-template struct and a field-by-field setter, and 2.1× faster than
goFAST's reflection path. goFAST's `Receiver` path allocates less. Neither
library is close to the limit of what Go can do: both allocate on every
field.

### Multiple cores

Decoders share nothing but the read-only `Registry`, so you can run one
`Decoder` per feed or channel on its own goroutine. Total packets per second:

```
cores                    1        2        4        6        10
default GC            334k     559k     808k     645k     572k
GOGC=off              326k     583k    1.15M    1.44M    1.88M
```

With the default GC, throughput peaks at 4 cores (2.4×) and then falls. With
GC off it keeps rising to 5.7× at 10 cores. The ceiling is allocation and GC
pressure, not contention inside the decoder. Every message and sequence entry
is a `map[string]any`. For multi-feed deployments, raise `GOGC` or set
`GOMEMLIMIT`. Cutting allocations is the main planned optimization.

### Small messages

```
decode Logon (35 B)     195 ns     ~5M messages/s      12 allocs
encode Logon            131 ns     ~7.6M messages/s     8 allocs
```

### Reproduce

```
go test -run '^$' -bench . -benchmem                     # fastfix only
cd benchmarks && go test -bench . -benchmem              # vs goFAST
cd benchmarks && go test -bench Parallel -cpu 1,2,4,6,10 # multi-core
```

The comparison lives in its own module under `benchmarks/`, so fastfix
itself has no dependencies.

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
