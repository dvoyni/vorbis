package vorbis

import "math/bits"

type huffmanCode struct {
	tree  []uint32
	table [256]uint32 // (value<<4)|length, 0 = fallback to tree
}

func (h *huffmanCode) Lookup(r *bitReader) uint32 {
	if r.bitsLeft < 8 {
		r.refill()
	}
	if r.bitsLeft >= 8 {
		entry := h.table[uint8(r.buf)]
		if entry != 0 {
			r.buf >>= entry & 0xf
			r.bitsLeft -= uint(entry & 0xf)
			return entry >> 4
		}
	}
	// fallback: tree walk for codes longer than 8 bits
	i := uint32(0)
	for i&1 == 0 {
		i = h.tree[i+r.Read1()]
	}
	return i >> 1
}

// huffmanBuilder assigns codewords the way the Vorbis I specification
// prescribes (section 3.2.1): entries are taken in order, and each one gets the
// lowest codeword of its length that no earlier codeword uses or is a prefix
// of. It builds the tree Lookup walks, in which a node is a pair of slots, one
// per bit, holding either a leaf (entry*2+1) or the index of the next node,
// and the table Lookup tries first.
type huffmanBuilder struct {
	tree  []uint32
	table [256]uint32
	next  uint32 // index of the first node not yet allocated
	// marker[l] is the lowest codeword of length l still available, for
	// l in 1..32; the same bookkeeping as libvorbis's _make_words.
	marker [33]uint32
}

func newHuffmanBuilder(size uint32) *huffmanBuilder {
	return &huffmanBuilder{
		tree: make([]uint32, size),
		next: 2, // the root occupies 0 and 1
	}
}

// Put assigns entry the next codeword of the given length and inserts it in
// the tree. The most significant bit of a codeword is the first bit read.
func (t *huffmanBuilder) Put(entry uint32, length uint8) {
	code := t.marker[length]
	if code>>length != 0 {
		// no codeword of this length is left; the lengths describe an
		// overpopulated tree, and the entry is dropped as before
		return
	}

	// Advance the marker of this length past the codeword, and the markers
	// of shorter lengths past any node the codeword completed: when the
	// codeword was the second child of its parent, the parent is used up as
	// well, and so on up the tree.
	for l := length; l > 0; l-- {
		if t.marker[l]&1 != 0 {
			if l == 1 {
				t.marker[1]++
			} else {
				t.marker[l] = t.marker[l-1] << 1
			}
			break
		}
		t.marker[l]++
	}

	// The markers of longer lengths that were hanging below the codeword
	// move to the subtree below the marker that just advanced.
	node := code
	for l := length + 1; l < 33; l++ {
		if t.marker[l]>>1 != node {
			break
		}
		node = t.marker[l]
		t.marker[l] = t.marker[l-1] << 1
	}

	// Insert the codeword, allocating nodes down its path.
	index := uint32(0)
	for bit := length - 1; bit > 0; bit-- {
		slot := index + (code>>bit)&1
		if t.tree[slot] == 0 {
			t.tree[slot] = t.newNode()
		}
		index = t.tree[slot]
	}
	t.tree[index+code&1] = entry*2 + 1

	// A codeword of up to 8 bits resolves through the table: Lookup indexes
	// it by the next 8 bits of the stream, first bit read in bit 0, so the
	// codeword lands there reversed, and every entry whose low bits are the
	// codeword is its.
	if length <= 8 {
		reversed := uint32(bits.Reverse8(uint8(code))) >> (8 - length)
		packed := entry<<4 | uint32(length)
		for i := reversed; i < 256; i += 1 << length {
			t.table[i] = packed
		}
	}
}

// newNode allocates the next node of the tree. Nodes are allocated in
// increasing order and put fills a node's first slot as soon as it allocates
// it, so the nodes in use are always the prefix [0, next): the first free node
// is next, and scanning the tree for it (as findEmpty used to) only walked over
// every node allocated so far, which made building a codebook quadratic.
func (t *huffmanBuilder) newNode() uint32 {
	node := t.next
	t.next += 2
	return node
}

func (t *huffmanBuilder) build() *huffmanCode {
	return &huffmanCode{tree: t.tree, table: t.table}
}
