package vorbis

import "errors"

type floor interface {
	Decode(*bitReader, []codebook, uint32) interface{}
	// Apply renders the floor curve over out. scratch belongs to the calling
	// Decoder, so that a floor read once can be applied from many Decoders at
	// once.
	Apply(out []float32, data interface{}, scratch *floor1Scratch)
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

func (s *Setup) readSetupHeader(header []byte) error {
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
	s.floor1Points = 0
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
			if len(f.xList) > s.floor1Points {
				s.floor1Points = len(f.xList)
			}
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
				m.magnitude[i] = r.Read8(ilog(s.channels - 1))
				m.angle[i] = r.Read8(ilog(s.channels - 1))
			}
		}
		if r.Read8(2) != 0 {
			return errors.New("vorbis: decoding error")
		}
		m.mux = make([]uint8, s.channels)
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
	s.initLookup()
	return nil
}

// initLookup builds the tables that depend only on the blocksizes. They are
// read-only once built, which is what lets a Setup be shared.
func (s *Setup) initLookup() {
	s.windows[0] = makeWindow(s.blocksize[0])
	s.windows[1] = makeWindow(s.blocksize[1])
	generateIMDCTLookup(s.blocksize[0], &s.lookup[0])
	generateIMDCTLookup(s.blocksize[1], &s.lookup[1])
}
