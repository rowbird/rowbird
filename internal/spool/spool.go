// Package spool stores a run's result on local disk while the run is processed, so that formats
// are generated from one copy of the rows without holding them in memory (docs/spec/03-flows.md,
// section 5).
//
// A spool file is the magic "RBSPOOL1", the columns as length-prefixed JSON, then one record per
// row: the number of values followed by each value as a type tag and its payload. The row
// encoding is deterministic, so the SHA-256 of the columns and rows identifies a result (the
// "changed" condition compares these hashes).
package spool

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/plugin"
)

const magic = "RBSPOOL1"

// Value tags.
const (
	tagNull byte = iota
	tagInt
	tagFloat
	tagText
	tagBool
	tagBinary
	tagTime
	tagDecimal
	tagDate
	tagTimeOfDay
	tagJSON
)

// Path is where a run's result is spooled inside dir.
func Path(dir string, runID uuid.UUID) string { return filepath.Join(dir, runID.String()+".spool") }

// ErrFormat is returned for a file that is not a spool.
var ErrFormat = errors.New("spool: invalid file")

// Writer appends rows to a spool file.
type Writer struct {
	f    *os.File
	buf  *bufio.Writer
	hash hash.Hash
	rec  []byte
	rows int64
}

// Create starts a spool at path with the result's columns.
func Create(path string, cols []plugin.Column) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // the runner builds the path inside the spool dir
	if err != nil {
		return nil, fmt.Errorf("spool: create: %w", err)
	}
	w := &Writer{f: f, buf: bufio.NewWriterSize(f, 64<<10), hash: sha256.New()}
	head, err := json.Marshal(cols)
	if err != nil {
		_ = w.Abort()
		return nil, err
	}
	w.rec = binary.AppendUvarint(append([]byte(magic), nil...), uint64(len(head)))
	w.rec = append(w.rec, head...)
	if _, err := w.buf.Write(w.rec); err != nil {
		_ = w.Abort()
		return nil, err
	}
	w.hash.Write(w.rec[len(magic):])
	return w, nil
}

// Write appends one row.
func (w *Writer) Write(row []any) error {
	rec := binary.AppendUvarint(w.rec[:0], uint64(len(row)))
	for _, v := range row {
		var err error
		if rec, err = appendValue(rec, v); err != nil {
			return err
		}
	}
	w.rec = rec
	w.hash.Write(rec)
	w.rows++
	_, err := w.buf.Write(rec)
	return err
}

// Rows is the number of rows written.
func (w *Writer) Rows() int64 { return w.rows }

// Hash is the hex SHA-256 of the columns and the rows written so far.
func (w *Writer) Hash() string { return hex.EncodeToString(w.hash.Sum(nil)) }

// Close flushes and closes the file.
func (w *Writer) Close() error {
	err := w.buf.Flush()
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Abort closes and removes the file.
func (w *Writer) Abort() error {
	_ = w.f.Close()
	return os.Remove(w.f.Name())
}

func appendValue(b []byte, v any) ([]byte, error) {
	switch x := v.(type) {
	case nil:
		return append(b, tagNull), nil
	case int64:
		return binary.AppendVarint(append(b, tagInt), x), nil
	case float64:
		return binary.BigEndian.AppendUint64(append(b, tagFloat), math.Float64bits(x)), nil
	case bool:
		if x {
			return append(b, tagBool, 1), nil
		}
		return append(b, tagBool, 0), nil
	case string:
		return appendBytes(append(b, tagText), []byte(x)), nil
	case []byte:
		return appendBytes(append(b, tagBinary), x), nil
	case time.Time:
		t, err := x.MarshalBinary()
		if err != nil {
			return nil, err
		}
		return appendBytes(append(b, tagTime), t), nil
	case plugin.Decimal:
		return appendBytes(append(b, tagDecimal), []byte(x)), nil
	case plugin.Date:
		return appendBytes(append(b, tagDate), []byte(x)), nil
	case plugin.TimeOfDay:
		return appendBytes(append(b, tagTimeOfDay), []byte(x)), nil
	case plugin.JSON:
		return appendBytes(append(b, tagJSON), []byte(x)), nil
	}
	return nil, fmt.Errorf("spool: unsupported value type %T", v)
}

func appendBytes(b, v []byte) []byte {
	return append(binary.AppendUvarint(b, uint64(len(v))), v...)
}

// Reader reads a spool file.
type Reader struct {
	f    *os.File
	r    *bufio.Reader
	cols []plugin.Column
	row  []any
	err  error
}

// Open opens a spool and reads its columns.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path) //nolint:gosec // the runner builds the path inside the spool dir
	if err != nil {
		return nil, fmt.Errorf("spool: open: %w", err)
	}
	r := &Reader{f: f, r: bufio.NewReaderSize(f, 64<<10)}
	head := make([]byte, len(magic))
	if _, err := io.ReadFull(r.r, head); err != nil || string(head) != magic {
		_ = f.Close()
		return nil, ErrFormat
	}
	b, err := r.bytes()
	if err == nil {
		err = json.Unmarshal(b, &r.cols)
	}
	if err != nil {
		_ = f.Close()
		return nil, ErrFormat
	}
	return r, nil
}

// Columns returns the result's columns.
func (r *Reader) Columns() []plugin.Column { return r.cols }

// Next reads the next row; it returns false at the end or on error (see Err).
func (r *Reader) Next() bool {
	n, err := binary.ReadUvarint(r.r)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			r.err = ErrFormat
		}
		return false
	}
	if n > uint64(len(r.cols)) {
		r.err = ErrFormat
		return false
	}
	r.row = make([]any, n)
	for i := range r.row {
		if r.row[i], err = r.value(); err != nil {
			r.err = ErrFormat
			return false
		}
	}
	return true
}

// Row returns the current row.
func (r *Reader) Row() []any { return r.row }

// Err returns the error that stopped Next, if any.
func (r *Reader) Err() error { return r.err }

// Close closes the file.
func (r *Reader) Close() error { return r.f.Close() }

func (r *Reader) bytes() ([]byte, error) {
	n, err := binary.ReadUvarint(r.r)
	if err != nil {
		return nil, err
	}
	if n > 1<<30 {
		return nil, ErrFormat
	}
	b := make([]byte, n)
	_, err = io.ReadFull(r.r, b)
	return b, err
}

func (r *Reader) value() (any, error) {
	tag, err := r.r.ReadByte()
	if err != nil {
		return nil, err
	}
	switch tag {
	case tagNull:
		return nil, nil
	case tagInt:
		return binary.ReadVarint(r.r)
	case tagFloat:
		var b [8]byte
		if _, err := io.ReadFull(r.r, b[:]); err != nil {
			return nil, err
		}
		return math.Float64frombits(binary.BigEndian.Uint64(b[:])), nil
	case tagBool:
		b, err := r.r.ReadByte()
		return b == 1, err
	}
	b, err := r.bytes()
	if err != nil {
		return nil, err
	}
	switch tag {
	case tagText:
		return string(b), nil
	case tagBinary:
		return b, nil
	case tagTime:
		var t time.Time
		return t, t.UnmarshalBinary(b)
	case tagDecimal:
		return plugin.Decimal(b), nil
	case tagDate:
		return plugin.Date(b), nil
	case tagTimeOfDay:
		return plugin.TimeOfDay(b), nil
	case tagJSON:
		return plugin.JSON(b), nil
	}
	return nil, ErrFormat
}
