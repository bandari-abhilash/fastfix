import java.io.FileInputStream;
import java.io.InputStream;
import java.lang.management.ManagementFactory;
import java.nio.file.Files;
import java.nio.file.Paths;
import java.util.Arrays;

import org.openfast.Context;
import org.openfast.DecimalValue;
import org.openfast.GroupValue;
import org.openfast.Message;
import org.openfast.SequenceValue;
import org.openfast.codec.FastDecoder;
import org.openfast.template.MessageTemplate;
import org.openfast.template.loader.XMLMessageTemplateLoader;

/**
 * Decodes testdata/increfresh.bin with OpenFAST, matching the Go benchmark's
 * per-packet work: reset dictionaries, decode one message, sum px*size.
 */
public class OpenFastBench {
    static final long WANT = 9260955000L;

    /** Unsynchronized in-memory stream; rewind() replays the same packet. */
    static final class PacketStream extends InputStream {
        final byte[] buf;
        int pos;
        PacketStream(byte[] buf) { this.buf = buf; }
        void rewind() { pos = 0; }
        @Override public int read() { return pos < buf.length ? buf[pos++] & 0xff : -1; }
        @Override public int read(byte[] b, int off, int len) {
            if (pos >= buf.length) return -1;
            int n = Math.min(len, buf.length - pos);
            System.arraycopy(buf, pos, b, off, n);
            pos += n;
            return n;
        }
    }

    final PacketStream in;
    final Context ctx = new Context();
    final FastDecoder decoder;

    OpenFastBench(String templatesPath, byte[] pkt) throws Exception {
        XMLMessageTemplateLoader loader = new XMLMessageTemplateLoader();
        loader.setLoadTemplateIdFromAuxId(true); // register templates under their XML id attribute
        try (FileInputStream f = new FileInputStream(templatesPath)) {
            loader.load(f);
        }
        ctx.setTemplateRegistry(loader.getTemplateRegistry());
        in = new PacketStream(pkt);
        decoder = new FastDecoder(ctx, in);
    }

    /** One packet: fresh dictionary state, decode, checksum. */
    long decodeOne() {
        in.rewind();
        decoder.reset(); // FastDecoder.reset() -> Context.reset(): clears every dictionary
        Message msg = decoder.readMessage();
        SequenceValue entries = msg.getSequence("MDEntries");
        long sum = 0;
        for (int i = 0, n = entries.getLength(); i < n; i++) {
            GroupValue e = entries.get(i);
            DecimalValue px = (DecimalValue) e.getScalar("MDEntryPx");
            sum += mantissaAtExp(px, -2) * e.getLong("MDEntrySize");
        }
        return sum;
    }

    /** Exact mantissa rescaled to the given exponent. */
    static long mantissaAtExp(DecimalValue d, int exp) {
        long m = d.mantissa;
        for (int e = d.exponent; e > exp; e--) m = Math.multiplyExact(m, 10);
        for (int e = d.exponent; e < exp; e++) {
            if (m % 10 != 0) throw new ArithmeticException("inexact: " + d);
            m /= 10;
        }
        return m;
    }

    public static void main(String[] args) throws Exception {
        String dir = args.length > 0 ? args[0] : "../testdata";
        byte[] pkt = Files.readAllBytes(Paths.get(dir, "increfresh.bin"));
        OpenFastBench b = new OpenFastBench(dir + "/templates.xml", pkt);

        long first = b.decodeOne();
        if (first != WANT) {
            System.err.printf("checksum mismatch: got %d, want %d%n", first, WANT);
            System.exit(1);
        }
        if (b.in.pos != pkt.length) {
            System.err.printf("decoder consumed %d of %d bytes%n", b.in.pos, pkt.length);
            System.exit(1);
        }
        System.out.printf("OpenFAST: checksum OK (%d), packet %d bytes%n", first, pkt.length);

        long sink = 0;
        long warmEnd = System.nanoTime() + 5_000_000_000L;
        int warm = 0;
        while (warm < 2_000_000 || System.nanoTime() < warmEnd) { sink += b.decodeOne(); warm++; }

        // Size the timed runs to ~2s from a 0.5s calibration.
        long t0 = System.nanoTime(); int cal = 0;
        while (System.nanoTime() - t0 < 500_000_000L) { sink += b.decodeOne(); cal++; }
        int iters = (int) (cal * 4L);

        com.sun.management.ThreadMXBean mx =
            (com.sun.management.ThreadMXBean) ManagementFactory.getThreadMXBean();
        long tid = Thread.currentThread().getId();
        double[] ns = new double[6];
        for (int r = 0; r < ns.length; r++) {
            long a0 = mx.getThreadAllocatedBytes(tid);
            long s = System.nanoTime();
            for (int i = 0; i < iters; i++) sink += b.decodeOne();
            long el = System.nanoTime() - s;
            long alloc = mx.getThreadAllocatedBytes(tid) - a0;
            ns[r] = (double) el / iters;
            System.out.printf("run %d: %d iters, %.1f ns/packet, %.0f B/packet%n",
                r + 1, iters, ns[r], (double) alloc / iters);
        }
        double[] sorted = ns.clone();
        Arrays.sort(sorted);
        System.out.printf("median: %.1f ns/packet%n", (sorted[2] + sorted[3]) / 2);
        System.out.printf("sink: %d (warmup %d iters)%n", sink, warm);
    }
}
