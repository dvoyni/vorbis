package vorbis

import "errors"

// floorData is one channel's decoded floor for the current packet. The
// Decoder owns one per channel and the floors fill the same buffers packet
// after packet, so decoding allocates nothing once they have grown.
type floorData struct {
	floor     floor
	decoded   bool // the channel carries audio; Apply uses the fields below
	noResidue bool

	// floor 0
	amplitude    uint32
	coefficients []float32
	// floor 1, with the scratch Apply works in, which is per stream so that
	// the floor itself is never written while decoding
	y      []uint32
	step2  []bool
	finalY []uint32
}

func (d *Decoder) decodePacket(r *bitReader, out []float32) ([]float32, error) {
	if r.ReadBool() {
		return nil, errors.New("vorbis: decoding error")
	}
	modeNumber := r.Read8(ilog(len(d.modes) - 1))
	mode := d.modes[modeNumber]
	// decode window type
	blocktype := mode.blockflag
	longWindow := mode.blockflag == 1
	blocksize := d.blocksize[blocktype]
	spectrumSize := uint32(blocksize / 2)
	windowPrev, windowNext := false, false
	window := windowType{blocksize, blocksize, blocksize}
	if longWindow {
		windowPrev = r.ReadBool()
		windowNext = r.ReadBool()
		if !windowPrev {
			window.prev = d.blocksize[0]
		}
		if !windowNext {
			window.next = d.blocksize[0]
		}
	}

	mapping := &d.mappings[mode.mapping]
	if d.floorBuffer == nil {
		d.floorBuffer = make([]floorData, d.channels)
	}
	for ch := range d.residueBuffer {
		d.residueBuffer[ch] = d.residueBuffer[ch][:spectrumSize]
		for i := range d.residueBuffer[ch] {
			d.residueBuffer[ch][i] = 0
		}
	}

	d.decodeFloors(r, d.floorBuffer, mapping, spectrumSize)
	d.decodeResidue(r, d.residueBuffer, mapping, d.floorBuffer, spectrumSize)
	d.inverseCoupling(mapping, d.residueBuffer)
	d.applyFloor(d.floorBuffer, d.residueBuffer)

	// inverse MDCT
	for ch := range d.rawBuffer {
		d.rawBuffer[ch] = d.rawBuffer[ch][:blocksize]
		imdct(d.lookup[blocktype], d.residueBuffer[ch], d.rawBuffer[ch])
	}

	// apply window and overlap
	d.applyWindow(&window, d.rawBuffer)
	center := blocksize / 2
	offset := d.blocksize[1]/4 - d.blocksize[0]/4
	n := 0
	if d.hasOverlap {
		n = blocksize / 2
		if longWindow && !windowPrev {
			n -= offset
		}
		if !longWindow && !d.overlapShort {
			n += offset
		}
		if out == nil {
			out = make([]float32, n*d.channels)
		}
	}
	if longWindow {
		start := 0
		if !windowPrev {
			start = offset
		}
		if d.hasOverlap {
			for ch := range d.rawBuffer {
				for i := 0; i < center-start; i++ {
					out[i*d.channels+ch] = d.rawBuffer[ch][start+i] + d.overlap[(start+i)*d.channels+ch]
				}
			}
		}
		d.overlapShort = false
	} else /*short window*/ {
		if d.hasOverlap {
			if d.overlapShort {
				for ch := range d.rawBuffer {
					for i := 0; i < center; i++ {
						out[i*d.channels+ch] = d.rawBuffer[ch][i] + d.overlap[(offset+i)*d.channels+ch]
					}
				}
			} else {
				for i := 0; i < offset*d.channels; i++ {
					out[i] = d.overlap[i]
				}
				for ch := range d.rawBuffer {
					for i := offset; i < offset+center; i++ {
						out[i*d.channels+ch] = d.rawBuffer[ch][i-offset] + d.overlap[i*d.channels+ch]
					}
				}
			}
		}
		d.overlapShort = true
	}

	if !d.hasOverlap {
		n = 0
	}
	overlapCenter := d.blocksize[1] / 4
	oStart := overlapCenter - center/2
	oEnd := overlapCenter + center/2
	for i := 0; i < oStart*d.channels; i++ {
		d.overlap[i] = 0
	}
	for ch := range d.rawBuffer {
		for i := oStart; i < oEnd; i++ {
			d.overlap[i*d.channels+ch] = d.rawBuffer[ch][center+i-oStart]
		}
	}
	for i := oEnd * d.channels; i < len(d.overlap); i++ {
		d.overlap[i] = 0
	}
	d.hasOverlap = true

	return out[:n*d.channels], nil
}

func (d *Decoder) decodeFloors(r *bitReader, floors []floorData, mapping *mapping, n uint32) {
	for ch := range floors {
		data := &floors[ch]
		data.floor = d.floors[mapping.submaps[mapping.mux[ch]].floor]
		data.decoded = data.floor.Decode(r, d.codebooks, n, data)
		data.noResidue = !data.decoded
	}

	for i := 0; i < int(mapping.couplingSteps); i++ {
		if !floors[mapping.magnitude[i]].noResidue || !floors[mapping.angle[i]].noResidue {
			floors[mapping.magnitude[i]].noResidue = false
			floors[mapping.angle[i]].noResidue = false
		}
	}
}

func (d *Decoder) decodeResidue(r *bitReader, out [][]float32, mapping *mapping, floors []floorData, n uint32) {
	for i := range mapping.submaps {
		doNotDecode := d.submapSkip[:0]
		tmp := d.submapVectors[:0]
		for j := 0; j < d.channels; j++ {
			if mapping.mux[j] == uint8(i) {
				doNotDecode = append(doNotDecode, floors[j].noResidue)
				tmp = append(tmp, out[j])
			}
		}
		d.submapSkip, d.submapVectors = doNotDecode, tmp
		d.classifications = d.residues[mapping.submaps[i].residue].Decode(r, doNotDecode, n, d.codebooks, tmp, d.classifications)
	}
}

func (d *Decoder) inverseCoupling(mapping *mapping, residueVectors [][]float32) {
	for i := mapping.couplingSteps; i > 0; i-- {
		magnitudeVector := residueVectors[mapping.magnitude[i-1]]
		angleVector := residueVectors[mapping.angle[i-1]]
		for j := range magnitudeVector {
			m := magnitudeVector[j]
			a := angleVector[j]
			if m > 0 {
				if a > 0 {
					m, a = m, m-a
				} else {
					a, m = m, m+a
				}
			} else {
				if a > 0 {
					m, a = m, m+a
				} else {
					a, m = m, m-a
				}
			}
			magnitudeVector[j] = m
			angleVector[j] = a
		}
	}
}

func (d *Decoder) applyFloor(floors []floorData, residueVectors [][]float32) {
	for ch := range residueVectors {
		if floors[ch].decoded {
			floors[ch].floor.Apply(residueVectors[ch], &floors[ch])
		} else {
			for i := range residueVectors[ch] {
				residueVectors[ch][i] = 0
			}
		}
	}
}
