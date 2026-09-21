package vorbis

import (
	"testing"
	"time"
)

// testdata/long.gob holds the three headers and every audio packet of a 20 s
// mono 44.1 kHz clip (oggvorbis's testdata/long.ogg, demuxed). test.gob is a
// short clip, so setup dominates any whole-clip figure taken from it; long.gob
// separates the cost of the setup header from the cost of decoding.

func readLongFile(b *testing.B) *GobVorbis {
	b.Helper()
	data, err := readGobFile("testdata/long.gob")
	if err != nil {
		b.Fatal(err)
	}
	return data
}

// BenchmarkHeaders measures each stage of reading the headers: the
// identification and comment headers alone, then the setup header on a decoder
// that has already read those two.
func BenchmarkHeaders(b *testing.B) {
	data := readLongFile(b)

	b.Run("identification+comment", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var dec Decoder
			if err := dec.ReadHeader(data.Headers[0]); err != nil {
				b.Fatal(err)
			}
			if err := dec.ReadHeader(data.Headers[1]); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("setup", func(b *testing.B) {
		var base Decoder
		if err := base.ReadHeader(data.Headers[0]); err != nil {
			b.Fatal(err)
		}
		if err := base.ReadHeader(data.Headers[1]); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			dec := base
			if err := dec.ReadHeader(data.Headers[2]); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func setupLong(b *testing.B, data *GobVorbis) *Decoder {
	b.Helper()
	dec := new(Decoder)
	for _, header := range data.Headers {
		if err := dec.ReadHeader(header); err != nil {
			b.Fatal(err)
		}
	}
	return dec
}

// BenchmarkDecodePacket decodes one audio packet per iteration into a reused
// buffer, cycling through the clip so short and long blocks are both counted.
func BenchmarkDecodePacket(b *testing.B) {
	data := readLongFile(b)
	dec := setupLong(b, data)
	buffer := make([]float32, dec.BufferSize())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := dec.DecodeInto(data.Packets[i%len(data.Packets)], buffer); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecodeClip decodes the whole clip per iteration into a reused
// buffer and reports how many seconds of audio that yields per second.
func BenchmarkDecodeClip(b *testing.B) {
	data := readLongFile(b)
	dec := setupLong(b, data)
	buffer := make([]float32, dec.BufferSize())

	b.ReportAllocs()
	b.ResetTimer()
	start := time.Now()
	samples := 0
	for i := 0; i < b.N; i++ {
		dec.Clear()
		samples = 0
		for _, packet := range data.Packets {
			out, err := dec.DecodeInto(packet, buffer)
			if err != nil {
				b.Fatal(err)
			}
			samples += len(out) / dec.Channels()
		}
	}
	elapsed := time.Since(start)
	b.StopTimer()
	audio := float64(samples) / float64(dec.SampleRate())
	b.ReportMetric(audio*float64(b.N)/elapsed.Seconds(), "x-realtime")
}
