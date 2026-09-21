package vorbis

import (
	"bytes"
	"errors"
	"sync"
)

type floor interface {
	// Decode reads one channel's floor into data and reports whether the
	// channel carries audio in this packet.
	Decode(r *bitReader, books []codebook, n uint32, data *floorData) bool
	Apply(out []float32, data *floorData)
}

type mapping struct {
	couplingSteps uint16
	angle         []uint8
	magnitude     []uint8
	mux           []uint8
	submaps       []mappingSubmap
}

type mappingSubmap struct {
	floor, residue uint8
}

type mode struct {
	blockflag uint8
	mapping   uint8
}

// setup is everything a setup header describes. Once read it is never
// written to again: decoding only reads it, so any number of Decoders may
// share one and decode on their own goroutines.
type setup struct {
	codebooks []codebook
	floors    []floor
	residues  []residue
	mappings  []mapping
	modes     []mode
}

// setups holds every setup header this process has read, so that a stream
// whose setup header was seen before shares the codebooks, floors, residues,
// mappings and modes already parsed instead of parsing them again. The setup
// header describes the encoder configuration rather than the audio, so every
// clip from one encoder at one quality level carries the same one, and a
// program playing many streams of a few clips parses a handful of setups
// instead of one per stream. Entries are never evicted.
var setups struct {
	sync.Mutex
	m map[uint64]*cachedSetup
}

type cachedSetup struct {
	setup
	// channels and header are what the setup was read from: parsing the
	// mappings depends on the channel count, and nothing else in the
	// identification header matters to it.
	channels int
	header   []byte
}

// setupFor returns the setup the setup header describes for a stream of the
// given channel count, parsing it only if the process has not read an
// identical one before.
func setupFor(header []byte, channels int) (*setup, error) {
	key := fingerprint(header, channels)
	setups.Lock()
	c := setups.m[key]
	setups.Unlock()
	if c != nil && c.channels == channels && bytes.Equal(c.header, header) {
		return &c.setup, nil
	}

	c = &cachedSetup{channels: channels, header: append([]byte(nil), header...)}
	if err := c.read(header, channels); err != nil {
		return nil, err
	}
	setups.Lock()
	if setups.m == nil {
		setups.m = make(map[uint64]*cachedSetup)
	}
	if setups.m[key] == nil {
		setups.m[key] = c
	}
	setups.Unlock()
	return &c.setup, nil
}

// fingerprint is FNV-1a over the channel count and the setup header.
func fingerprint(header []byte, channels int) uint64 {
	h := uint64(14695981039346656037)
	h = (h ^ uint64(channels)) * 1099511628211
	for _, b := range header {
		h = (h ^ uint64(b)) * 1099511628211
	}
	return h
}

// useSetup makes s the Decoder's setup and allocates the buffers its stream
// needs, which are the Decoder's own.
func (d *Decoder) useSetup(s *setup) {
	d.setup = *s
	d.initLookup()
	d.overlap = make([]float32, d.blocksize[1]*d.channels)
	d.setupRead = true
}

func (s *setup) read(header []byte, channels int) error {
	r := newBitReader(header)

	// CODEBOOKS
	s.codebooks = make([]codebook, r.Read16(8)+1)
	for i := range s.codebooks {
		err := s.codebooks[i].ReadFrom(r)
		if err != nil {
			return err
		}
	}

	// TIME DOMAIN TRANSFORMS
	transformCount := r.Read8(6) + 1
	for i := 0; i < int(transformCount); i++ {
		if r.Read16(16) != 0 {
			return errors.New("vorbis: decoding error")
		}
	}

	// FLOORS
	s.floors = make([]floor, r.Read8(6)+1)
	for i := range s.floors {
		var err error
		switch r.Read16(16) {
		case 0:
			f := new(floor0)
			err = f.ReadFrom(r)
			s.floors[i] = f
		case 1:
			f := new(floor1)
			err = f.ReadFrom(r)
			s.floors[i] = f
		default:
			return errors.New("vorbis: decoding error")
		}
		if err != nil {
			return err
		}
	}

	// RESIDUES
	s.residues = make([]residue, r.Read8(6)+1)
	for i := range s.residues {
		err := s.residues[i].ReadFrom(r)
		if err != nil {
			return err
		}
	}

	// MAPPINGS
	s.mappings = make([]mapping, r.Read8(6)+1)
	for i := range s.mappings {
		m := &s.mappings[i]
		if r.Read16(16) != 0 {
			return errors.New("vorbis: decoding error")
		}
		if r.ReadBool() {
			m.submaps = make([]mappingSubmap, r.Read8(4)+1)
		} else {
			m.submaps = make([]mappingSubmap, 1)
		}
		if r.ReadBool() {
			m.couplingSteps = r.Read16(8) + 1
			m.magnitude = make([]uint8, m.couplingSteps)
			m.angle = make([]uint8, m.couplingSteps)
			for i := range m.magnitude {
				m.magnitude[i] = r.Read8(ilog(channels - 1))
				m.angle[i] = r.Read8(ilog(channels - 1))
			}
		}
		if r.Read8(2) != 0 {
			return errors.New("vorbis: decoding error")
		}
		m.mux = make([]uint8, channels)
		if len(m.submaps) > 1 {
			for i := range m.mux {
				m.mux[i] = r.Read8(4)
			}
		}
		for i := range m.submaps {
			r.Read8(8)
			m.submaps[i].floor = r.Read8(8)
			m.submaps[i].residue = r.Read8(8)
		}
	}

	// MODES
	s.modes = make([]mode, r.Read8(6)+1)
	for i := range s.modes {
		m := &s.modes[i]
		m.blockflag = r.Read8(1)
		if r.Read16(16) != 0 {
			return errors.New("vorbis: decoding error")
		}
		if r.Read16(16) != 0 {
			return errors.New("vorbis: decoding error")
		}
		m.mapping = r.Read8(8)
	}

	if !r.ReadBool() {
		return errors.New("vorbis: decoding error")
	}
	return nil
}

func (d *Decoder) initLookup() {
	d.windows[0] = makeWindow(d.blocksize[0])
	d.windows[1] = makeWindow(d.blocksize[1])
	generateIMDCTLookup(d.blocksize[0], &d.lookup[0])
	generateIMDCTLookup(d.blocksize[1], &d.lookup[1])
	d.residueBuffer = make([][]float32, d.channels)
	for i := range d.residueBuffer {
		d.residueBuffer[i] = make([]float32, d.blocksize[1]/2)
	}
	d.rawBuffer = make([][]float32, d.channels)
	for i := range d.rawBuffer {
		d.rawBuffer[i] = make([]float32, d.blocksize[1])
	}
}
