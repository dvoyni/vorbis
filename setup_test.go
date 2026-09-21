package vorbis

import (
	"sync"
	"testing"
)

func readLongOrTest(t testing.TB) *GobVorbis {
	t.Helper()
	data, err := readGobFile("testdata/long.gob")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// decodeAll decodes every packet with dec and returns the interleaved samples.
func decodeAll(t testing.TB, dec *Decoder, data *GobVorbis) []float32 {
	t.Helper()
	buffer := make([]float32, dec.BufferSize())
	var out []float32
	for _, packet := range data.Packets {
		got, err := dec.DecodeInto(packet, buffer)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, got...)
	}
	return out
}

// TestSetupSharedAcrossDecoders is the claim the Setup makes: decoders built
// from one Setup and run on their own goroutines at once each produce exactly
// what a decoder that read its own headers produces.
func TestSetupSharedAcrossDecoders(t *testing.T) {
	data := readLongOrTest(t)

	var own Decoder
	for _, header := range data.Headers {
		if err := own.ReadHeader(header); err != nil {
			t.Fatal(err)
		}
	}
	want := decodeAll(t, &own, data)

	setup, err := ReadSetup(data.Headers[0], data.Headers[1], data.Headers[2])
	if err != nil {
		t.Fatal(err)
	}
	if setup.SampleRate() != own.SampleRate() || setup.Channels() != own.Channels() || setup.BufferSize() != own.BufferSize() {
		t.Fatalf("ReadSetup reports %d Hz x%d buffer %d; ReadHeader reported %d Hz x%d buffer %d",
			setup.SampleRate(), setup.Channels(), setup.BufferSize(), own.SampleRate(), own.Channels(), own.BufferSize())
	}
	if own.Setup() == nil {
		t.Fatal("a Decoder that read its own headers has no Setup to share")
	}

	const decoders = 8
	got := make([][]float32, decoders)
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i] = decodeAll(t, NewDecoder(setup), data)
		}(i)
	}
	wg.Wait()

	for i, samples := range got {
		if len(samples) != len(want) {
			t.Fatalf("decoder %d produced %d samples, want %d", i, len(samples), len(want))
		}
		for j := range samples {
			if samples[j] != want[j] {
				t.Fatalf("decoder %d differs from the reference at sample %d: %v vs %v", i, j, samples[j], want[j])
			}
		}
	}
}

// TestSetupMatches: the fingerprint tells the stream a Setup was read from
// apart from another, and ignores the comment header.
func TestSetupMatches(t *testing.T) {
	data := readLongOrTest(t)
	setup, err := ReadSetup(data.Headers[0], data.Headers[1], data.Headers[2])
	if err != nil {
		t.Fatal(err)
	}
	if !setup.Matches(data.Headers[0], data.Headers[2]) {
		t.Error("a Setup does not match its own headers")
	}
	other := append([]byte(nil), data.Headers[2]...)
	other[len(other)-2] ^= 1
	if setup.Matches(data.Headers[0], other) {
		t.Error("a Setup matches a setup header that differs by one bit")
	}
	if setup.Matches(data.Headers[2], data.Headers[2]) {
		t.Error("a Setup matches with its identification header swapped out")
	}
}

// TestReadSetupRejectsPartial: the three headers must all be present and be
// what they claim.
func TestReadSetupRejectsPartial(t *testing.T) {
	data := readLongOrTest(t)
	if _, err := ReadSetup(data.Headers[0], data.Headers[1], data.Headers[1]); err == nil {
		t.Error("ReadSetup accepted a comment header in place of the setup header")
	}
	if _, err := ReadSetup(data.Headers[1], data.Headers[1], data.Headers[2]); err == nil {
		t.Error("ReadSetup accepted a stream with no identification header")
	}
	if _, err := ReadSetup(nil, data.Headers[1], data.Headers[2]); err == nil {
		t.Error("ReadSetup accepted a nil header")
	}
}

// BenchmarkNewDecoder is what a second stream on a clip already set up costs:
// the per-stream buffers alone.
func BenchmarkNewDecoder(b *testing.B) {
	data := readLongOrTest(b)
	setup, err := ReadSetup(data.Headers[0], data.Headers[1], data.Headers[2])
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		NewDecoder(setup)
	}
}

// BenchmarkReadSetup is the once-per-clip cost the split leaves behind, for
// comparison with BenchmarkHeaders/setup.
func BenchmarkReadSetup(b *testing.B) {
	data := readLongOrTest(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := ReadSetup(data.Headers[0], data.Headers[1], data.Headers[2]); err != nil {
			b.Fatal(err)
		}
	}
}
