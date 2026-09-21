package vorbis

import "testing"

// TestDecodeIntoAllocatesNothing pins that decoding is allocation-free once
// the Decoder's scratch has grown: a streaming consumer calls DecodeInto for
// every packet of every voice, so any allocation here scales with the number
// of voices playing.
func TestDecodeIntoAllocatesNothing(t *testing.T) {
	data, err := readTestFile()
	if err != nil {
		t.Fatal(err)
	}
	var dec Decoder
	for _, header := range data.Headers {
		if err := dec.ReadHeader(header); err != nil {
			t.Fatal(err)
		}
	}
	buffer := make([]float32, dec.BufferSize())
	decodeAll := func() {
		dec.Clear()
		for _, packet := range data.Packets {
			if _, err := dec.DecodeInto(packet, buffer); err != nil {
				t.Fatal(err)
			}
		}
	}
	decodeAll() // the first packets of each shape grow the scratch
	if allocs := testing.AllocsPerRun(10, decodeAll); allocs != 0 {
		t.Errorf("decoding the clip allocates %v times, expected 0", allocs)
	}
}
