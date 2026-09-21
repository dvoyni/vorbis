package vorbis

import "errors"

// A Setup is everything a vorbis stream's three headers say: the format, the
// comments, and the codebooks, floors, residues, mappings and modes the setup
// header carries, with the window and IMDCT tables derived from the blocksizes.
//
// It is a pure function of the headers. Once ReadSetup returns, a Setup is
// never written to again, so any number of Decoders may be built from one and
// used concurrently, each on its own goroutine, without synchronisation.
type Setup struct {
	sampleRate int
	channels   int
	blocksize  [2]int
	Bitrate    Bitrate
	CommentHeader

	codebooks []codebook
	floors    []floor
	residues  []residue
	mappings  []mapping
	modes     []mode

	windows [2][]float32
	lookup  [2]imdctLookup

	// floor1Points is the longest x list among the floors, which sizes the
	// scratch a Decoder applies a floor1 curve with.
	floor1Points int

	// identification and setup are fingerprints of the two header packets that
	// decoding depends on, so a container can tell whether a stream's own
	// headers are the ones this Setup was read from. The comment header is
	// left out: a re-tagged copy of the same encode decodes with the same Setup.
	identification, setup uint64

	headerRead bool
	setupRead  bool
}

// ReadSetup parses the three vorbis headers, in the order the stream carries
// them: identification, comment, setup.
func ReadSetup(identification, comment, setup []byte) (*Setup, error) {
	s := new(Setup)
	for _, header := range [][]byte{identification, comment, setup} {
		if err := s.readHeader(header); err != nil {
			return nil, err
		}
	}
	if !s.complete() {
		return nil, errors.New("vorbis: missing headers")
	}
	return s, nil
}

// SampleRate returns the sample rate of the vorbis stream.
func (s *Setup) SampleRate() int { return s.sampleRate }

// Channels returns the number of channels of the vorbis stream.
func (s *Setup) Channels() int { return s.channels }

// BufferSize returns the highest amount of data that can be decoded from a single packet.
// The result is already multiplied with the number of channels.
func (s *Setup) BufferSize() int { return s.blocksize[1] / 2 * s.channels }

// Matches reports whether identification and setup are the header packets this
// Setup was read from, so that a Decoder built from it decodes the stream they
// begin. The comment header is not part of the comparison.
func (s *Setup) Matches(identification, setup []byte) bool {
	return s.identification == fingerprint(identification) && s.setup == fingerprint(setup)
}

func (s *Setup) complete() bool { return s.headerRead && s.setupRead }

func (s *Setup) readHeader(header []byte) error {
	if !IsHeader(header) {
		return errors.New("vorbis: invalid header")
	}
	headerType := header[0]
	body := header[7:]
	switch headerType {
	case headerTypeIdentification:
		if err := s.readIdentificationHeader(body); err != nil {
			return err
		}
		s.identification = fingerprint(header)
		s.headerRead = true
	case headerTypeComment:
		return s.readCommentHeader(body)
	case headerTypeSetup:
		if err := s.readSetupHeader(body); err != nil {
			return err
		}
		s.setup = fingerprint(header)
		s.setupRead = true
	default:
		return errors.New("vorbis: unknown header type")
	}
	return nil
}

// fingerprint is FNV-1a over a header packet.
func fingerprint(b []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

// A Decoder stores the information necessary to decode a vorbis steam.
//
// It is either built from a Setup with NewDecoder, or reads its own headers
// with ReadHeader, in which case it parses a Setup of its own. Either way it
// owns only what one stream needs: the overlap and the working buffers.
type Decoder struct {
	setup *Setup
	// pending collects the headers ReadHeader has been given so far, until
	// they are complete and become setup.
	pending *Setup

	Bitrate Bitrate
	CommentHeader

	overlap      []float32
	hasOverlap   bool
	overlapShort bool

	residueBuffer [][]float32
	floorBuffer   []floorData
	rawBuffer     [][]float32
	floorScratch  floor1Scratch
}

// NewDecoder creates a Decoder over a Setup. The Setup's tables are shared
// with every other Decoder built from it and never written to.
func NewDecoder(setup *Setup) *Decoder {
	d := new(Decoder)
	d.attach(setup)
	return d
}

// Setup returns the Setup this Decoder decodes with, or nil if the headers
// have not been read yet. Any number of Decoders may be built from it.
func (d *Decoder) Setup() *Setup { return d.setup }

func (d *Decoder) attach(s *Setup) {
	d.setup = s
	d.pending = nil
	d.Bitrate = s.Bitrate
	d.CommentHeader = s.CommentHeader
	d.overlap = make([]float32, s.blocksize[1]*s.channels)
	d.hasOverlap = false
	d.residueBuffer = make([][]float32, s.channels)
	for i := range d.residueBuffer {
		d.residueBuffer[i] = make([]float32, s.blocksize[1]/2)
	}
	d.rawBuffer = make([][]float32, s.channels)
	for i := range d.rawBuffer {
		d.rawBuffer[i] = make([]float32, s.blocksize[1])
	}
	d.floorBuffer = make([]floorData, s.channels)
	d.floorScratch = floor1Scratch{
		step2:  make([]bool, s.floor1Points),
		finalY: make([]uint32, s.floor1Points),
	}
}

// The Bitrate of a vorbis stream.
// Some or all of the fields can be zero.
type Bitrate struct {
	Nominal int
	Minimum int
	Maximum int
}

// The CommentHeader of a vorbis stream.
type CommentHeader struct {
	Vendor   string
	Comments []string
}

// headers is whichever Setup holds what the Decoder knows so far.
func (d *Decoder) headers() *Setup {
	if d.setup != nil {
		return d.setup
	}
	return d.pending
}

// SampleRate returns the sample rate of the vorbis stream.
// This will be zero if the headers have not been read yet.
func (d *Decoder) SampleRate() int {
	if s := d.headers(); s != nil {
		return s.sampleRate
	}
	return 0
}

// Channels returns the number of channels of the vorbis stream.
// This will be zero if the headers have not been read yet.
func (d *Decoder) Channels() int {
	if s := d.headers(); s != nil {
		return s.channels
	}
	return 0
}

// BufferSize returns the highest amount of data that can be decoded from a single packet.
// The result is already multiplied with the number of channels.
// This will be zero if the headers have not been read yet.
func (d *Decoder) BufferSize() int {
	if s := d.headers(); s != nil {
		return s.BufferSize()
	}
	return 0
}

// IsHeader returns wether the packet is a vorbis header.
func IsHeader(packet []byte) bool {
	return len(packet) > 6 && packet[0]&1 == 1 &&
		packet[1] == 'v' &&
		packet[2] == 'o' &&
		packet[3] == 'r' &&
		packet[4] == 'b' &&
		packet[5] == 'i' &&
		packet[6] == 's'
}

// ReadHeader reads a vorbis header.
// Three headers (identification, comment, and setup) must be read before any samples can be decoded.
func (d *Decoder) ReadHeader(header []byte) error {
	if d.pending == nil {
		d.pending = new(Setup)
	}
	if err := d.pending.readHeader(header); err != nil {
		return err
	}
	d.Bitrate = d.pending.Bitrate
	d.CommentHeader = d.pending.CommentHeader
	if d.pending.complete() {
		d.attach(d.pending)
	}
	return nil
}

// HeadersRead returns wether the headers necessary for decoding have been read.
func (d *Decoder) HeadersRead() bool {
	return d.setup != nil
}

// Decode decodes a packet and returns the result as an interleaved float slice.
// The number of samples decoded varies and can be zero, but will be at most BufferSize()
func (d *Decoder) Decode(in []byte) ([]float32, error) {
	if !d.HeadersRead() {
		return nil, errors.New("vorbis: missing headers")
	}
	return d.decodePacket(newBitReader(in), nil)
}

// DecodeInto decodes a packet and stores the result in the given buffer.
// The size of the buffer must be at least BufferSize().
// The method will always return a slice of the buffer or nil.
func (d *Decoder) DecodeInto(in []byte, buffer []float32) ([]float32, error) {
	if !d.HeadersRead() {
		return nil, errors.New("vorbis: missing headers")
	}
	if len(buffer) < d.BufferSize() {
		return nil, errors.New("vorbis: buffer too short")
	}
	return d.decodePacket(newBitReader(in), buffer)
}

// Clear must be called between decoding two non-consecutive packets.
func (d *Decoder) Clear() {
	d.hasOverlap = false
}
