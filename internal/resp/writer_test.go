package resp

import (
	"bytes"
	"testing"
)

func TestWriter(t *testing.T) {
	tests := []struct {
		name  string
		write func(w *Writer) error
		want  string
	}{
		{
			name:  "simple string",
			write: func(w *Writer) error { return w.WriteSimpleString("OK") },
			want:  "+OK\r\n",
		},
		{
			name:  "simple string with CRLF is sanitized",
			write: func(w *Writer) error { return w.WriteSimpleString("a\r\nb") },
			want:  "+a  b\r\n",
		},
		{
			name:  "error",
			write: func(w *Writer) error { return w.WriteError("ERR unknown command") },
			want:  "-ERR unknown command\r\n",
		},
		{
			// A client could put \r\n in a command name to inject a fake reply
			name:  "error with injected reply is sanitized",
			write: func(w *Writer) error { return w.WriteError("ERR unknown command 'FOO\r\n+OK'") },
			want:  "-ERR unknown command 'FOO  +OK'\r\n",
		},
		{
			name:  "integer",
			write: func(w *Writer) error { return w.WriteInteger(5) },
			want:  ":5\r\n",
		},
		{
			name:  "negative integer",
			write: func(w *Writer) error { return w.WriteInteger(-5) },
			want:  ":-5\r\n",
		},
		{
			name:  "bulk string",
			write: func(w *Writer) error { return w.WriteBulkString("Ali") },
			want:  "$3\r\nAli\r\n",
		},
		{
			name:  "empty bulk string",
			write: func(w *Writer) error { return w.WriteBulkString("") },
			want:  "$0\r\n\r\n",
		},
		{
			// Bulk strings are length-prefixed, so CRLF inside is kept as is
			name:  "bulk string with CRLF is kept",
			write: func(w *Writer) error { return w.WriteBulkString("a\r\nb") },
			want:  "$4\r\na\r\nb\r\n",
		},
		{
			name:  "null",
			write: func(w *Writer) error { return w.WriteNull() },
			want:  "$-1\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := NewWriter(&buf)

			if err := tt.write(w); err != nil {
				t.Fatalf("write: unexpected error: %v", err)
			}
			if err := w.Flush(); err != nil {
				t.Fatalf("flush: unexpected error: %v", err)
			}

			if got := buf.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// bufio.Writer keeps data in memory until Flush, so the client sees nothing before it.
func TestWriterFlush(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)

	if err := w.WriteSimpleString("PONG"); err != nil {
		t.Fatalf("write: unexpected error: %v", err)
	}
	if got := buf.String(); got != "" {
		t.Errorf("before flush: got %q, want empty", got)
	}

	if err := w.Flush(); err != nil {
		t.Fatalf("flush: unexpected error: %v", err)
	}
	if got, want := buf.String(), "+PONG\r\n"; got != want {
		t.Errorf("after flush: got %q, want %q", got, want)
	}
}

func TestWriteReply(t *testing.T) {
	tests := []struct {
		name  string
		reply Reply
		want  string
	}{
		{name: "simple string", reply: SimpleString("PONG"), want: "+PONG\r\n"},
		{name: "error", reply: Error("ERR unknown command"), want: "-ERR unknown command\r\n"},
		{name: "integer", reply: Integer(-5), want: ":-5\r\n"},
		{name: "bulk string", reply: BulkString("Ali"), want: "$3\r\nAli\r\n"},
		{name: "null", reply: Null{}, want: "$-1\r\n"},
		{name: "empty array", reply: Array{}, want: "*0\r\n"},
		{
			name:  "array",
			reply: Array{BulkString("Ali"), Null{}, Integer(5)},
			want:  "*3\r\n$3\r\nAli\r\n$-1\r\n:5\r\n",
		},
		{
			name:  "nested array",
			reply: Array{Array{SimpleString("OK")}, BulkString("")},
			want:  "*2\r\n*1\r\n+OK\r\n$0\r\n\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := NewWriter(&buf)

			if err := w.WriteReply(tt.reply); err != nil {
				t.Fatalf("write: unexpected error: %v", err)
			}
			if err := w.Flush(); err != nil {
				t.Fatalf("flush: unexpected error: %v", err)
			}

			if got := buf.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWriteReplyNil(t *testing.T) {
	w := NewWriter(&bytes.Buffer{})
	if err := w.WriteReply(nil); err == nil {
		t.Error("expected error for nil reply, got nil")
	}
}
