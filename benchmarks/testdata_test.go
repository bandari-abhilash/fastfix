package benchmarks

import (
	"bytes"
	"flag"
	"os"
	"testing"
)

var writeTestdata = flag.Bool("write-testdata", false, "rewrite testdata/ for the other-language harnesses")

// The Java, Rust and C++ harnesses decode testdata/increfresh.bin with
// testdata/templates.xml and must print this checksum: the sum over all
// entries of MDEntryPx mantissa (exponent -2) times MDEntrySize.
const wantChecksum = 9260955000

func TestTestdataUpToDate(t *testing.T) {
	pkt := packet(t)
	if *writeTestdata {
		if err := os.WriteFile("testdata/increfresh.bin", pkt, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("testdata/templates.xml", []byte(templatesXML), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile("testdata/increfresh.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, pkt) {
		t.Fatal("testdata/increfresh.bin is stale; rerun with -write-testdata")
	}
	var sum int64
	for i := 0; i < numEntries; i++ {
		sum += (2245050 + int64(i)*5) * wantSize(i)
	}
	if sum != wantChecksum {
		t.Fatalf("checksum = %d, want %d", sum, wantChecksum)
	}
}
