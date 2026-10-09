//! fastlib (Rust) benchmark: per packet, reset the dictionary, decode one
//! MDIncRefresh message, and sum MDEntryPx (mantissa at exp -2) * MDEntrySize.
use fastlib::{Decimal, Decoder, MessageFactory, Value};
use serde::Deserialize;
use std::alloc::{GlobalAlloc, Layout, System};
use std::hint::black_box;
use std::sync::atomic::{AtomicU64, Ordering::Relaxed};
use std::time::{Duration, Instant};

const WANT: i64 = 9_260_955_000;

// ---- counting allocator ----
struct Counting;
static ALLOCS: AtomicU64 = AtomicU64::new(0);
static BYTES: AtomicU64 = AtomicU64::new(0);
unsafe impl GlobalAlloc for Counting {
    unsafe fn alloc(&self, l: Layout) -> *mut u8 {
        ALLOCS.fetch_add(1, Relaxed);
        BYTES.fetch_add(l.size() as u64, Relaxed);
        unsafe { System.alloc(l) }
    }
    unsafe fn dealloc(&self, p: *mut u8, l: Layout) {
        unsafe { System.dealloc(p, l) }
    }
    unsafe fn realloc(&self, p: *mut u8, l: Layout, n: usize) -> *mut u8 {
        ALLOCS.fetch_add(1, Relaxed);
        BYTES.fetch_add(n as u64, Relaxed);
        unsafe { System.realloc(p, l, n) }
    }
}
#[global_allocator]
static GLOBAL: Counting = Counting;

/// Rescale a decimal to an exact integer mantissa at exponent -2.
fn mantissa_e2(d: &Decimal) -> i64 {
    let (mut m, mut e) = (d.mantissa, d.exponent);
    while e > -2 { m *= 10; e -= 1; }
    while e < -2 { m /= 10; e += 1; }
    m
}

// ---- style 1: MessageFactory callbacks (fields delivered by name) ----
#[derive(Default)]
struct SumFactory { sum: i64, px: i64, size: i64 }

impl MessageFactory for SumFactory {
    fn start_template(&mut self, _: u32, _: &str) {}
    fn stop_template(&mut self) {}
    fn set_value(&mut self, _: u32, name: &str, value: Option<Value>) {
        match (name, value) {
            ("MDEntryPx", Some(Value::Decimal(d))) => self.px = mantissa_e2(&d),
            ("MDEntrySize", Some(Value::Int64(v))) => self.size = v,
            _ => {}
        }
    }
    fn start_sequence(&mut self, _: u32, _: &str, _: u32) {}
    fn start_sequence_item(&mut self, _: u32) { self.px = 0; self.size = 0; }
    fn stop_sequence_item(&mut self) { self.sum += self.px * self.size; }
    fn stop_sequence(&mut self) {}
    fn start_group(&mut self, _: &str) {}
    fn stop_group(&mut self) {}
    fn start_template_ref(&mut self, _: &str, _: bool) {}
    fn stop_template_ref(&mut self) {}
}

fn decode_callback(dec: &mut Decoder, pkt: &[u8]) -> i64 {
    dec.reset();
    let mut f = SumFactory::default();
    dec.decode_slice(pkt, &mut f).expect("decode");
    f.sum
}

// ---- style 2: serde into typed structs ----
#[derive(Deserialize)]
enum Message { MDIncRefresh(IncRefresh) }

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
#[allow(dead_code)]
struct IncRefresh {
    message_type: String,
    msg_seq_num: u32,
    sending_time: u64,
    #[serde(rename = "MDEntries")]
    md_entries: Vec<Entry>,
}

#[derive(Deserialize)]
#[allow(dead_code, non_snake_case)]
struct Entry {
    MDUpdateAction: u32,
    MDEntryType: String,
    SecurityID: u32,
    RptSeq: u32,
    MDEntryPx: Option<Decimal>,
    MDEntrySize: Option<i64>,
    MDPriceLevel: Option<u32>,
}

fn decode_serde(dec: &mut Decoder, pkt: &[u8]) -> i64 {
    dec.reset();
    let Message::MDIncRefresh(m) = fastlib::from_slice(dec, pkt).expect("decode");
    m.md_entries.iter()
        .map(|e| e.MDEntryPx.as_ref().map_or(0, mantissa_e2) * e.MDEntrySize.unwrap_or(0))
        .sum()
}

/// fastlib 0.3.8 rejects field instructions without an `id` attribute (FAST 1.1
/// makes `id` optional). Ids are only metadata here: dictionary keys use names and
/// ids are never on the wire, so we add unique ids in memory. The file is unchanged.
fn add_field_ids(xml: &str) -> String {
    let tags = ["string", "uInt32", "int32", "uInt64", "int64", "decimal", "byteVector", "length"];
    let (mut out, mut next) = (String::new(), 1u32);
    let mut rest = xml;
    while let Some(i) = rest.find('<') {
        out.push_str(&rest[..=i]);
        rest = &rest[i + 1..];
        let tag = rest.split(|c: char| c.is_whitespace() || c == '>' || c == '/').next().unwrap();
        let head = &rest[..rest.find('>').unwrap_or(rest.len())];
        if tags.contains(&tag) && !head.contains(" id=") {
            out.push_str(&format!("{tag} id=\"{next}\""));
            rest = &rest[tag.len()..];
            next += 1;
        }
    }
    out.push_str(rest);
    out
}

// ---- harness ----
fn bench(label: &str, dec: &mut Decoder, pkt: &[u8], f: fn(&mut Decoder, &[u8]) -> i64) {
    let first = f(dec, pkt);
    if first != WANT {
        eprintln!("{label}: checksum {first} != {WANT}");
        std::process::exit(1);
    }
    let run = |d: Duration, dec: &mut Decoder| {
        let (start, mut n, mut sum) = (Instant::now(), 0u64, 0i64);
        while start.elapsed() < d {
            for _ in 0..1000 { sum = sum.wrapping_add(f(dec, black_box(pkt))); }
            n += 1000;
        }
        (start.elapsed().as_nanos() as f64 / n as f64, n, black_box(sum))
    };
    run(Duration::from_secs(1), dec); // warm-up
    let (a0, b0) = (ALLOCS.load(Relaxed), BYTES.load(Relaxed));
    let mut times = Vec::new();
    let (mut total_n, mut total_sum) = (0u64, 0i64);
    for i in 1..=6 {
        let (ns, n, s) = run(Duration::from_secs(2), dec);
        println!("  {label} run {i}: {ns:8.1} ns/packet");
        times.push(ns);
        total_n += n;
        total_sum = total_sum.wrapping_add(s);
    }
    let (a, b) = (ALLOCS.load(Relaxed) - a0, BYTES.load(Relaxed) - b0);
    times.sort_by(|x, y| x.partial_cmp(y).unwrap());
    println!("{label}: median {:.1} ns/packet | {:.1} allocs/packet, {:.0} B/packet | checksum ok ({first}/packet), accumulated {total_sum} over {total_n} packets",
        (times[2] + times[3]) / 2.0, a as f64 / total_n as f64, b as f64 / total_n as f64);
}

fn main() {
    let dir = std::env::args().nth(1).unwrap_or_else(|| "../testdata".into());
    let xml = std::fs::read_to_string(format!("{dir}/templates.xml")).expect("templates.xml");
    let pkt = std::fs::read(format!("{dir}/increfresh.bin")).expect("increfresh.bin");
    let mut dec = Decoder::new_from_xml(&add_field_ids(&xml)).expect("templates");

    // Diagnostic: decoding the packet twice without reset (dictionary carries over).
    dec.reset();
    let mut f = SumFactory::default();
    dec.decode_slice(&pkt, &mut f).expect("decode");
    let mut g = SumFactory::default();
    let second = dec.decode_slice(&pkt, &mut g).map(|_| g.sum);
    println!("no-reset diagnostic: 1st decode {}, 2nd decode without reset {:?}", f.sum, second);

    println!("fastlib 0.3.8, packet {} bytes, reset via Decoder::reset() each packet", pkt.len());
    bench("fastlib callback (MessageFactory)", &mut dec, &pkt, decode_callback);
    bench("fastlib serde typed struct      ", &mut dec, &pkt, decode_serde);
}
