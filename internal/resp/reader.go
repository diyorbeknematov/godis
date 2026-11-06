package resp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

var ErrProtocol = errors.New("resp: protocol error")

// Limits protect the server from clients that announce huge lengths
// and make us allocate memory we never get data for.
const (
	maxArrayLen = 1024 * 1024       // max number of arguments in a command
	maxBulkLen  = 512 * 1024 * 1024 // max size of a single argument (same as Redis)
)

type Reader struct {
	rd *bufio.Reader
}

func NewReader(r io.Reader) *Reader {
	return &Reader{
		rd: bufio.NewReader(r),
	}
}

// Buffered returns how many bytes were read from the source but not consumed yet.
// The AOF loader uses it to find where the last complete command ended.
func (r *Reader) Buffered() int {
	return r.rd.Buffered()
}

func (r *Reader) ReadCommand() ([]string, error) {
	line, err := r.readLine()
	if err != nil {
		return nil, err
	}

	if len(line) == 0 || line[0] != '*' {
		return nil, fmt.Errorf("%w: expected '*', got %q", ErrProtocol, line)
	}

	return r.readArray(line)
}

func (r *Reader) readLine() (string, error) {
	line, err := r.rd.ReadString('\n')
	if err != nil {
		return "", err
	}

	// Line must end with "\r\n"
	if !strings.HasSuffix(line, "\r\n") {
		return "", fmt.Errorf("%w: expected '\\r\\n', got %q", ErrProtocol, line)
	}
	line = strings.TrimSuffix(line, "\r\n")

	return line, nil
}

func (r *Reader) readArray(line string) ([]string, error) {
	count, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, fmt.Errorf("%w: invalid array length %q", ErrProtocol, line[1:])
	}

	if count < 0 {
		return nil, fmt.Errorf("%w: negative array length %d", ErrProtocol, count)
	}

	if count > maxArrayLen {
		return nil, fmt.Errorf("%w: array length %d exceeds limit %d", ErrProtocol, count, maxArrayLen)
	}

	args := make([]string, 0, count)
	for i := 0; i < count; i++ {
		arg, err := r.readBulkString()
		if err != nil {
			// EOF in the middle of a command is not a clean disconnect
			if errors.Is(err, io.EOF) {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		args = append(args, arg)
	}
	return args, nil
}

func (r *Reader) readBulkString() (string, error) {
	line, err := r.readLine()
	if err != nil {
		return "", err
	}

	if len(line) == 0 || line[0] != '$' {
		return "", fmt.Errorf("%w: expected '$', got %q", ErrProtocol, line)
	}

	argLen, err := strconv.Atoi(line[1:])
	if err != nil {
		return "", fmt.Errorf("%w: invalid bulk string length %q", ErrProtocol, line[1:])
	}

	if argLen < 0 {
		return "", fmt.Errorf("%w: negative bulk string length %d", ErrProtocol, argLen)
	}

	if argLen > maxBulkLen {
		return "", fmt.Errorf("%w: bulk string length %d exceeds limit %d", ErrProtocol, argLen, maxBulkLen)
	}

	arg := make([]byte, argLen+2) // +2 for \r\n
	_, err = io.ReadFull(r.rd, arg)
	if err != nil {
		return "", err
	}

	// Check only the last two bytes instead of converting the whole buffer
	if arg[argLen] != '\r' || arg[argLen+1] != '\n' {
		return "", fmt.Errorf("%w: expected '\\r\\n', got %q", ErrProtocol, arg[argLen:])
	}

	return string(arg[:argLen]), nil
}
