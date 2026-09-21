package vorbis

import (
	"encoding/binary"
	"errors"
)

const (
	headerTypeIdentification = 1
	headerTypeComment        = 3
	headerTypeSetup          = 5
)

func (s *Setup) readIdentificationHeader(h []byte) error {
	if len(h) <= 22 {
		return errors.New("vorbis: decoding error")
	}
	le := binary.LittleEndian
	version := le.Uint32(h)
	if version != 0 {
		return errors.New("vorbis: decoding error")
	}
	s.channels = int(h[4])
	s.sampleRate = int(le.Uint32(h[5:]))
	s.Bitrate.Maximum = int(le.Uint32(h[9:]))
	s.Bitrate.Nominal = int(le.Uint32(h[13:]))
	s.Bitrate.Minimum = int(le.Uint32(h[17:]))
	s.blocksize[0] = 1 << (h[21] & 0x0F)
	s.blocksize[1] = 1 << (h[21] >> 4)
	if h[22]&1 == 0 {
		return errors.New("vorbis: decoding error")
	}
	return nil
}

func (s *Setup) readCommentHeader(h []byte) error {
	var err error
	defer func() {
		if recover() != nil {
			err = errors.New("vorbis: decoding error")
		}
	}()
	le := binary.LittleEndian
	vendorLen := le.Uint32(h)
	h = h[4:]
	s.Vendor = string(h[:vendorLen])
	h = h[vendorLen:]
	numComments := int(le.Uint32(h))
	s.Comments = make([]string, numComments)
	h = h[4:]
	for i := 0; i < numComments; i++ {
		commentLen := le.Uint32(h)
		h = h[4:]
		s.Comments[i] = string(h[:commentLen])
		h = h[commentLen:]
	}
	return err
}
