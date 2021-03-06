// Copyright 2016 The Cockroach Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.
//
// Author: Radu Berinde (radu@cockroachlabs.com)

package sqlbase

import (
	"bytes"
	"fmt"

	"github.com/cockroachdb/cockroach/roachpb"
	"github.com/cockroachdb/cockroach/sql/parser"
	"github.com/cockroachdb/cockroach/util"
	"github.com/cockroachdb/cockroach/util/encoding"
)

// EncDatum represents a datum that is "backed" by an encoding and/or by a
// parser.Datum. It allows "passing through" a Datum without decoding and
// reencoding. TODO(radu): It will also allow comparing encoded datums directly
// (for certain encodings).
type EncDatum struct {
	Type ColumnType_Kind

	// Encoding type. Valid only if encoded is not nil.
	encoding DatumEncoding

	// Encoded datum (according to the encoding field).
	encoded []byte

	// Decoded datum.
	Datum parser.Datum
}

func (ed *EncDatum) stringWithAlloc(a *DatumAlloc) string {
	if ed.Datum == nil {
		if a == nil {
			a = &DatumAlloc{}
		}
		err := ed.Decode(a)
		if err != nil {
			return fmt.Sprintf("<error: %v>", err)
		}
	}
	return ed.Datum.String()
}

func (ed *EncDatum) String() string {
	return ed.stringWithAlloc(nil)
}

// SetEncoded initializes the EncDatum with the given encoded value. The encoded
// value is stored as a shallow copy, so the caller must make sure the slice is
// not modified for the lifetime of the EncDatum.
func (ed *EncDatum) SetEncoded(typ ColumnType_Kind, enc DatumEncoding, val []byte) {
	if len(val) == 0 {
		panic("empty encoded value")
	}
	ed.Type = typ
	ed.encoding = enc
	ed.encoded = val
	ed.Datum = nil
}

// SetFromBuffer initializes the EncDatum with an encoding that is possibly
// followed by other data. Similar to SetEncoded, except that this function
// figures out where the encoding stops and returns a slice for the rest of the
// buffer.
func (ed *EncDatum) SetFromBuffer(
	typ ColumnType_Kind, enc DatumEncoding, buf []byte,
) (remaining []byte, err error) {
	var encLen int
	switch enc {
	case DatumEncoding_ASCENDING_KEY, DatumEncoding_DESCENDING_KEY:
		encLen, err = encoding.PeekLength(buf)
	case DatumEncoding_VALUE:
		encLen, err = roachpb.PeekValueLength(buf)
	default:
		panic(fmt.Sprintf("unknown encoding %s", ed.encoding))
	}
	if err != nil {
		return nil, err
	}
	ed.SetEncoded(typ, enc, buf[:encLen])
	return buf[encLen:], nil
}

// SetDatum initializes the EncDatum with the given Datum.
func (ed *EncDatum) SetDatum(typ ColumnType_Kind, d parser.Datum) {
	if d == nil {
		panic("nil datum given")
	}
	if d != parser.DNull && !typ.ToDatumType().TypeEqual(d) {
		panic(fmt.Sprintf("invalid datum type given: %s, expected %s",
			d.Type(), typ.ToDatumType().Type()))
	}
	ed.Type = typ
	ed.encoded = nil
	ed.Datum = d
}

// IsUnset returns true if SetEncoded or SetDatum were not called.
func (ed *EncDatum) IsUnset() bool {
	return ed.encoded == nil && ed.Datum == nil
}

// Decode ensures that Datum is set (decoding if necessary).
func (ed *EncDatum) Decode(a *DatumAlloc) error {
	if ed.Datum != nil {
		return nil
	}
	if ed.encoded == nil {
		panic("decoding unset EncDatum")
	}
	datType := ed.Type.ToDatumType()
	var err error
	var rem []byte
	switch ed.encoding {
	case DatumEncoding_ASCENDING_KEY:
		ed.Datum, rem, err = DecodeTableKey(a, datType, ed.encoded, encoding.Ascending)
	case DatumEncoding_DESCENDING_KEY:
		ed.Datum, rem, err = DecodeTableKey(a, datType, ed.encoded, encoding.Descending)
	case DatumEncoding_VALUE:
		ed.Datum, rem, err = DecodeTableValue(a, datType, ed.encoded)
	default:
		panic(fmt.Sprintf("unknown encoding %s", ed.encoding))
	}
	if len(rem) != 0 {
		ed.Datum = nil
		return util.Errorf("%d trailing bytes in encoded value", len(rem))
	}
	return err
}

// Encoding returns the encoding that is already available (the latter indicated
// by the bool return value).
func (ed *EncDatum) Encoding() (DatumEncoding, bool) {
	if ed.encoded == nil {
		return 0, false
	}
	return ed.encoding, true
}

// Encode appends the encoded datum to the given slice using the requested
// encoding.
func (ed *EncDatum) Encode(a *DatumAlloc, enc DatumEncoding, appendTo []byte) ([]byte, error) {
	if ed.encoded != nil && enc == ed.encoding {
		// We already have an encoding that matches
		return append(appendTo, ed.encoded...), nil
	}
	if err := ed.Decode(a); err != nil {
		return nil, err
	}
	switch enc {
	case DatumEncoding_ASCENDING_KEY:
		return EncodeTableKey(appendTo, ed.Datum, encoding.Ascending)
	case DatumEncoding_DESCENDING_KEY:
		return EncodeTableKey(appendTo, ed.Datum, encoding.Descending)
	case DatumEncoding_VALUE:
		return EncodeTableValue(appendTo, ed.Datum)
	default:
		panic(fmt.Sprintf("unknown encoding requested %s", enc))
	}
}

// EncDatumRow is a row of EncDatums.
type EncDatumRow []EncDatum

func (r EncDatumRow) stringToBuf(a *DatumAlloc, b *bytes.Buffer) {
	b.WriteString("[")
	for i := range r {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(r[i].stringWithAlloc(a))
	}
	b.WriteString("]")
}

func (r EncDatumRow) String() string {
	var b bytes.Buffer
	r.stringToBuf(&DatumAlloc{}, &b)
	return b.String()
}

// EncDatumRows is a slice of EncDatumRows.
type EncDatumRows []EncDatumRow

func (r EncDatumRows) String() string {
	var a DatumAlloc
	var b bytes.Buffer
	b.WriteString("[")
	for i, r := range r {
		if i > 0 {
			b.WriteString(" ")
		}
		r.stringToBuf(&a, &b)
	}
	b.WriteString("]")
	return b.String()
}

// EncDatumRowAlloc is a helper that speeds up allocation of EncDatumRows
// (preferably of the same length).
type EncDatumRowAlloc struct {
	buf []EncDatum
	// Preallocate a small initial batch (helps cases where
	// we only allocate a few small rows).
	prealloc [16]EncDatum
}

// AllocRow allocates an EncDatumRow with the given number of columns.
func (a *EncDatumRowAlloc) AllocRow(cols int) EncDatumRow {
	if a.buf == nil {
		// First call.
		a.buf = a.prealloc[:]
	}
	if len(a.buf) < cols {
		// If the rows are small, allocate storage for a bunch of rows at once.
		bufLen := cols
		if cols <= 16 {
			bufLen *= 16
		} else if cols <= 64 {
			bufLen *= 4
		}
		a.buf = make([]EncDatum, bufLen)
	}
	// Chop off a row from buf, and limit its capacity to avoid corrupting the
	// following row in the unlikely case that the caller appends to the slice.
	result := EncDatumRow(a.buf[:cols:cols])
	a.buf = a.buf[cols:]
	return result
}

// CopyRow allocates an EncDatumRow and copies the given row to it.
func (a *EncDatumRowAlloc) CopyRow(row EncDatumRow) EncDatumRow {
	rowCopy := a.AllocRow(len(row))
	copy(rowCopy, row)
	return rowCopy
}
