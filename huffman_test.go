package vorbis

import "testing"

// packBits packs bit strings in the order the decoder reads them: the first
// character of the first string is the first bit read.
func packBits(bits ...string) []byte {
	var out []byte
	n := 0
	for _, s := range bits {
		for _, c := range s {
			if n%8 == 0 {
				out = append(out, 0)
			}
			if c == '1' {
				out[n/8] |= 1 << (n % 8)
			}
			n++
		}
	}
	return out
}

// checkCodewords builds a code from the entries' lengths and checks that each
// entry decodes from the codeword the Vorbis I specification assigns it, and
// from nothing more.
func checkCodewords(t *testing.T, lengths []uint8, codewords []string) {
	t.Helper()
	b := newHuffmanBuilder(uint32(len(lengths))*2 - 2)
	for entry, length := range lengths {
		b.Put(uint32(entry), length)
	}
	code := b.build()
	const sentinel = "10110010" // 0x4d read LSb first
	for entry, word := range codewords {
		r := newBitReader(packBits(word, sentinel))
		if got := code.Lookup(r); got != uint32(entry) {
			t.Errorf("codeword %s decodes to entry %d, want %d", word, got, entry)
		}
		if got := r.read(8); got != 0x4d {
			t.Errorf("after codeword %s the next byte reads %#x, want 0x4d", word, got)
		}
	}
}

// TestHuffmanSpecExample is the worked example of section 3.2.1.1 of the
// specification: codewords are assigned in entry order, each entry taking the
// lowest codeword of its length still available, so entry 5 gets 10 although
// entries 1 to 4 are longer.
func TestHuffmanSpecExample(t *testing.T) {
	checkCodewords(t,
		[]uint8{2, 4, 4, 4, 4, 2, 3, 3},
		[]string{"00", "0100", "0101", "0110", "0111", "10", "110", "111"})
}

// TestHuffmanLongCodewords exercises codewords the 8-bit table cannot resolve.
func TestHuffmanLongCodewords(t *testing.T) {
	checkCodewords(t,
		[]uint8{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 12},
		[]string{
			"0", "10", "110", "1110", "11110", "111110", "1111110", "11111110",
			"111111110", "1111111110", "11111111110", "111111111110", "111111111111",
		})
}
