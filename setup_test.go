package vorbis

import (
	"sync"
	"testing"
)

// forgetSetups empties the process-wide setup cache, so that the next stream
// parses its setup header again.
func forgetSetups() {
	setups.Lock()
	setups.m = nil
	setups.Unlock()
}

func readHeaders(t testing.TB, data *GobVorbis) *Decoder {
	t.Helper()
	dec := new(Decoder)
	for _, header := range data.Headers {
		if err := dec.ReadHeader(header); err != nil {
			t.Fatal(err)
		}
	}
	return dec
}

// readHeadersPrivately gives the Decoder a setup of its own that no other
// Decoder shares, the way every Decoder had one before the cache.
func readHeadersPrivately(t testing.TB, data *GobVorbis) *Decoder {
	t.Helper()
	dec := new(Decoder)
	for _, header := range data.Headers[:2] {
		if err := dec.ReadHeader(header); err != nil {
			t.Fatal(err)
		}
	}
	s := new(setup)
	if err := s.read(data.Headers[2][7:], dec.channels); err != nil {
		t.Fatal(err)
	}
	dec.useSetup(s)
	return dec
}

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

// TestSetupShared: a second stream with the same setup header gets the setup
// the first one parsed rather than a copy of it.
func TestSetupShared(t *testing.T) {
	data, err := readTestFile()
	if err != nil {
		t.Fatal(err)
	}
	forgetSetups()
	a, b := readHeaders(t, data), readHeaders(t, data)
	if &a.codebooks[0] != &b.codebooks[0] || a.floors[0] != b.floors[0] {
		t.Error("two streams with the same setup header parsed it twice")
	}
	if &a.overlap[0] == &b.overlap[0] || &a.rawBuffer[0][0] == &b.rawBuffer[0][0] {
		t.Error("two streams share buffers that belong to each stream")
	}
}

// TestSetupSharedAcrossDecoders is what makes sharing sound: decoders on one
// setup, decoding on their own goroutines at once, each produce exactly what a
// decoder with a setup of its own produces.
func TestSetupSharedAcrossDecoders(t *testing.T) {
	data, err := readTestFile()
	if err != nil {
		t.Fatal(err)
	}
	want := decodeAll(t, readHeadersPrivately(t, data), data)

	forgetSetups()
	const decoders = 8
	decs := make([]*Decoder, decoders)
	for i := range decs {
		decs[i] = readHeaders(t, data)
	}
	got := make([][]float32, decoders)
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i] = decodeAll(t, decs[i], data)
		}(i)
	}
	wg.Wait()

	for i, samples := range got {
		if len(samples) != len(want) {
			t.Fatalf("decoder %d produced %d samples, want %d", i, len(samples), len(want))
		}
		for j := range samples {
			if samples[j] != want[j] {
				t.Fatalf("decoder %d differs at sample %d: %v, want %v", i, j, samples[j], want[j])
			}
		}
	}
}

// TestSetupDistinguished: a setup header that differs by one bit, or the same
// one on a stream with another channel count, is not served from the cache.
func TestSetupDistinguished(t *testing.T) {
	data, err := readTestFile()
	if err != nil {
		t.Fatal(err)
	}
	forgetSetups()
	header := data.Headers[2][7:]
	s, err := setupFor(header, 1)
	if err != nil {
		t.Fatal(err)
	}
	other := append([]byte(nil), header...)
	other[len(other)-2] ^= 1
	if o, err := setupFor(other, 1); err == nil && o == s {
		t.Error("a setup header that differs by one bit was served the cached setup")
	}
	if o, err := setupFor(header, 2); err == nil && o == s {
		t.Error("a stereo stream was served the setup read for a mono one")
	}
	if o, err := setupFor(header, 1); err != nil || o != s {
		t.Error("the same setup header on the same channel count was parsed again")
	}
}

// BenchmarkOpen is what every stream after the first on a clip costs: its
// three headers, with the setup already in the cache.
func BenchmarkOpen(b *testing.B) {
	data, err := readTestFile()
	if err != nil {
		b.Fatal(err)
	}
	readHeaders(b, data)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		readHeaders(b, data)
	}
}
