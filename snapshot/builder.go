package snapshot

import (
	"encoding/binary"
	"hash/crc32"
	"sort"
)

// Builder assembles well-formed snapshot files. It is used by tests and by
// tools that need to produce snapshots; DeclaredCount may deliberately
// differ from the number of added records to manufacture count anomalies.
type Builder struct {
	sections map[SectionType]*sectionBuild
}

type sectionBuild struct {
	declared uint32
	records  [][]byte
}

// NewBuilder returns an empty Builder.
func NewBuilder() *Builder {
	return &Builder{sections: make(map[SectionType]*sectionBuild)}
}

func (b *Builder) sec(t SectionType) *sectionBuild {
	s, ok := b.sections[t]
	if !ok {
		s = &sectionBuild{}
		b.sections[t] = s
	}
	return s
}

// SetDeclaredCount overrides the declared record count of a section. By
// default the declared count equals the number of added records.
func (b *Builder) SetDeclaredCount(t SectionType, n int) *Builder {
	b.sec(t).declared = uint32(n)
	return b
}

// AddObject appends an object record to the object section.
func (b *Builder) AddObject(r ObjectRecord) *Builder {
	s := b.sec(SectionObjects)
	s.records = append(s.records, marshalRecord(&r))
	s.declared = uint32(len(s.records))
	return b
}

// AddLink appends a link record to the link section.
func (b *Builder) AddLink(r LinkRecord) *Builder {
	s := b.sec(SectionLinks)
	s.records = append(s.records, marshalRecord(&r))
	s.declared = uint32(len(s.records))
	return b
}

// AddAction appends an action record to the action section.
func (b *Builder) AddAction(r ActionRecord) *Builder {
	s := b.sec(SectionActions)
	s.records = append(s.records, marshalRecord(&r))
	s.declared = uint32(len(s.records))
	return b
}

// AddRawRecord appends a pre-encoded payload to a section without touching
// the declared count; useful for manufacturing malformed sections.
func (b *Builder) AddRawRecord(t SectionType, payload []byte) *Builder {
	s := b.sec(t)
	s.records = append(s.records, payload)
	return b
}

// Bytes serializes the snapshot. Sections are laid out in canonical order
// right after the directory.
func (b *Builder) Bytes() []byte {
	types := make([]SectionType, 0, len(b.sections))
	for t := range b.sections {
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })

	bodies := make(map[SectionType][]byte, len(types))
	for _, t := range types {
		s := b.sections[t]
		body := encodeSectionHeader(t, s.declared)
		for _, rec := range s.records {
			body = append(body, encodeRecord(rec)...)
		}
		bodies[t] = body
	}

	dirSize := len(types) * dirEntrySize
	offset := uint32(fileHeaderFixedSize + dirSize)
	dir := make([]byte, 0, dirSize)
	for _, t := range types {
		body := bodies[t]
		entry := make([]byte, dirEntrySize)
		entry[0] = byte(t)
		binary.LittleEndian.PutUint32(entry[4:8], offset)
		binary.LittleEndian.PutUint32(entry[8:12], uint32(len(body)))
		dir = append(dir, entry...)
		offset += uint32(len(body))
	}

	out := make([]byte, 0, offset)
	var fixed [fileHeaderFixedSize]byte
	binary.LittleEndian.PutUint32(fixed[0:4], magicNumber)
	binary.LittleEndian.PutUint16(fixed[4:6], formatVersion)
	binary.LittleEndian.PutUint16(fixed[6:8], uint16(len(types)))
	binary.LittleEndian.PutUint32(fixed[8:12], crc32.ChecksumIEEE(dir))
	out = append(out, fixed[:]...)
	out = append(out, dir...)
	for _, t := range types {
		out = append(out, bodies[t]...)
	}
	return out
}
