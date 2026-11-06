package resp

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// lineSanitizer replaces CR and LF with spaces so a single-line reply
// can never break the protocol framing.
var lineSanitizer = strings.NewReplacer("\r", " ", "\n", " ")

type Writer struct {
	bw *bufio.Writer
}

func NewWriter(w io.Writer) *Writer {
	return &Writer{
		bw: bufio.NewWriter(w),
	}
}

// WriteSimpleString writes "+OK\r\n". Any \r or \n in s is replaced with a space.
func (w *Writer) WriteSimpleString(s string) error {
	return w.writeLine('+', s)
}

// WriteError writes "-ERR unknown command\r\n".
// msg should start with the error kind, e.g. "ERR" or "WRONGTYPE".
// Error messages often contain client input, so \r and \n are replaced with spaces.
func (w *Writer) WriteError(msg string) error {
	return w.writeLine('-', msg)
}

// WriteInteger writes ":5\r\n".
func (w *Writer) WriteInteger(n int64) error {
	_, err := w.bw.WriteString(":" + strconv.FormatInt(n, 10) + "\r\n")
	return err
}

// WriteBulkString writes "$3\r\nAli\r\n". Unlike simple strings it is
// length-prefixed, so s may contain any bytes, including \r\n.
// Parts are written separately so s is not copied into a new string.
func (w *Writer) WriteBulkString(s string) error {
	if _, err := w.bw.WriteString("$" + strconv.Itoa(len(s)) + "\r\n"); err != nil {
		return err
	}
	if _, err := w.bw.WriteString(s); err != nil {
		return err
	}
	_, err := w.bw.WriteString("\r\n")
	return err
}

// WriteNull writes "$-1\r\n", the RESP2 reply for a missing key.
func (w *Writer) WriteNull() error {
	_, err := w.bw.WriteString("$-1\r\n")
	return err
}

// WriteReply writes r using the matching Write* method.
func (w *Writer) WriteReply(r Reply) error {
	switch v := r.(type) {
	case SimpleString:
		return w.WriteSimpleString(string(v))
	case Error:
		return w.WriteError(string(v))
	case Integer:
		return w.WriteInteger(int64(v))
	case BulkString:
		return w.WriteBulkString(string(v))
	case Null:
		return w.WriteNull()
	case Array:
		return w.writeArray(v)
	default:
		// Only reachable if a new Reply type is added without a case here
		return fmt.Errorf("resp: unsupported reply type %T", r)
	}
}

// writeArray writes the element count, then every element in turn.
// Elements may be arrays themselves.
func (w *Writer) writeArray(a Array) error {
	if _, err := w.bw.WriteString("*" + strconv.Itoa(len(a)) + "\r\n"); err != nil {
		return err
	}
	for _, r := range a {
		if err := w.WriteReply(r); err != nil {
			return err
		}
	}
	return nil
}

// Flush sends buffered data to the connection. Nothing reaches the
// client until Flush is called.
func (w *Writer) Flush() error {
	return w.bw.Flush()
}

func (w *Writer) writeLine(prefix byte, s string) error {
	if err := w.bw.WriteByte(prefix); err != nil {
		return err
	}
	if _, err := lineSanitizer.WriteString(w.bw, s); err != nil {
		return err
	}
	_, err := w.bw.WriteString("\r\n")
	return err
}
