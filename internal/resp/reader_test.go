package resp

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestReadCommand(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr error // nil means no error expected
	}{
		{
			name:  "single word",
			input: "*1\r\n$4\r\nPING\r\n",
			want:  []string{"PING"},
		},
		{
			name:  "three words",
			input: "*3\r\n$3\r\nSET\r\n$3\r\nism\r\n$3\r\nAli\r\n",
			want:  []string{"SET", "ism", "Ali"},
		},
		{
			name:  "empty bulk string",
			input: "*1\r\n$0\r\n\r\n",
			want:  []string{""},
		},
		{
			name:  "value with space",
			input: "*1\r\n$11\r\nsalom dunyo\r\n",
			want:  []string{"salom dunyo"},
		},
		{
			name:    "line without star",
			input:   "PING\r\n",
			wantErr: ErrProtocol,
		},
		{
			name:    "invalid bulk length",
			input:   "*1\r\n$abc\r\n",
			wantErr: ErrProtocol,
		},
		{
			name:    "negative bulk length",
			input:   "*1\r\n$-5\r\n",
			wantErr: ErrProtocol,
		},
		{
			name:  "empty array",
			input: "*0\r\n",
			want:  []string{},
		},
		{
			name:    "array length over limit",
			input:   "*1048577\r\n",
			wantErr: ErrProtocol,
		},
		{
			name:    "bulk length over limit",
			input:   "*1\r\n$536870913\r\n",
			wantErr: ErrProtocol,
		},
		{
			name:    "negative array length",
			input:   "*-1\r\n",
			wantErr: ErrProtocol,
		},
		{
			name:    "bulk not terminated by CRLF",
			input:   "*1\r\n$3\r\nabcde\r\n",
			wantErr: ErrProtocol,
		},
		{
			name:    "line ends with LF only",
			input:   "*1\n",
			wantErr: ErrProtocol,
		},
		{
			name:    "non-bulk element in array",
			input:   "*1\r\n:5\r\n",
			wantErr: ErrProtocol,
		},
		{
			// Client promised 2 elements but disconnected after the first one
			name:    "stream ends between elements",
			input:   "*2\r\n$4\r\nPING\r\n",
			wantErr: io.ErrUnexpectedEOF,
		},
		{
			// io.ReadFull wants 7 bytes ("ab" + 5 more) but the stream ends early
			name:    "less data than announced",
			input:   "*1\r\n$5\r\nab",
			wantErr: io.ErrUnexpectedEOF,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewReader(strings.NewReader(tt.input)).ReadCommand()

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// Over TCP several commands arrive in one stream, so the reader must stop
// exactly at the end of the first command and leave the rest for the next call.
func TestReadCommandSequential(t *testing.T) {
	input := "*1\r\n$4\r\nPING\r\n" + "*3\r\n$3\r\nSET\r\n$3\r\nism\r\n$3\r\nAli\r\n"
	r := NewReader(strings.NewReader(input))

	want := [][]string{
		{"PING"},
		{"SET", "ism", "Ali"},
	}

	for i, w := range want {
		got, err := r.ReadCommand()
		if err != nil {
			t.Fatalf("command %d: unexpected error: %v", i+1, err)
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("command %d: got %q, want %q", i+1, got, w)
		}
	}

	// Stream is fully consumed now
	if _, err := r.ReadCommand(); !errors.Is(err, io.EOF) {
		t.Errorf("after last command: error = %v, want io.EOF", err)
	}
}
