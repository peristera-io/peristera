// Package chunk splits a byte stream into content-defined chunks
// (Kamara ADR-0001 §1): boundaries are chosen by a gear-hash rolling
// function (FastCDC-style, single-tier with normalization), so inserting
// bytes reshapes only local chunks — the property that makes cross-version
// reuse and later delta-sync work. Single-tier sizing (R36): no boundary
// cliff. The chunker is content-only; hashing, encryption, and storage
// live in sibling packages.
package chunk

import (
	"bufio"
	"io"
)

// Size bounds (Kamara ADR-0001 §1). A chunk is at least MinSize (except a
// final short chunk), targets AvgSize, and never exceeds MaxSize.
const (
	MinSize = 256 * 1024
	AvgSize = 1024 * 1024
	MaxSize = 4 * 1024 * 1024

	avgBits = 20 // log2(AvgSize)
)

// Normalized chunking (FastCDC): a stricter mask before the average point
// biases toward longer chunks, a looser one after biases toward cutting —
// tightening the size distribution around AvgSize.
const (
	maskS uint64 = (1 << (avgBits + 2)) - 1 // stricter, used [MinSize, AvgSize)
	maskL uint64 = (1 << (avgBits - 2)) - 1 // looser, used [AvgSize, MaxSize)
)

// cutpoint returns the length of the first chunk in data (>= min unless
// data is shorter, <= max), using the gear-hash boundary condition.
func cutpoint(data []byte) int {
	n := len(data)
	if n <= MinSize {
		return n
	}
	if n > MaxSize {
		n = MaxSize
	}
	normal := AvgSize
	if n < normal {
		normal = n
	}
	var fp uint64
	i := MinSize
	for ; i < normal; i++ {
		fp = (fp << 1) + gear[data[i]]
		if fp&maskS == 0 {
			return i
		}
	}
	for ; i < n; i++ {
		fp = (fp << 1) + gear[data[i]]
		if fp&maskL == 0 {
			return i
		}
	}
	return n
}

// Split reads r to EOF, calling fn with each content-defined chunk in
// order. The slice passed to fn is only valid for the duration of the call
// (it is reused); copy it if retained. Streaming: at most ~MaxSize is held
// in memory at once, so large uploads don't buffer the whole file.
func Split(r io.Reader, fn func(chunk []byte) error) error {
	br := bufio.NewReaderSize(r, 1<<16)
	buf := make([]byte, 0, MaxSize)
	for {
		// Fill buf up to MaxSize (or EOF).
		for len(buf) < MaxSize {
			free := buf[len(buf):cap(buf)]
			m, err := br.Read(free)
			buf = buf[:len(buf)+m]
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if m == 0 {
				break
			}
		}
		if len(buf) == 0 {
			return nil
		}
		c := cutpoint(buf)
		if err := fn(buf[:c]); err != nil {
			return err
		}
		// Carry the remainder to the front.
		rest := copy(buf, buf[c:])
		buf = buf[:rest]
		if rest == 0 {
			// Nothing buffered; if the reader is drained we're done.
			if _, err := br.Peek(1); err == io.EOF {
				return nil
			}
		}
	}
}
