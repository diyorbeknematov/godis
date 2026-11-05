package resp

// Reply is a value a command can return to the client.
// The unexported method keeps the set of reply types closed to this package.
type Reply interface {
	reply()
}

// SimpleString is a short status reply, e.g. +OK.
type SimpleString string

// Error is an error reply; it should start with the error kind, e.g. "ERR ...".
type Error string

// Integer is a numeric reply, e.g. :5.
type Integer int64

// BulkString is a binary-safe string reply, e.g. $3\r\nAli.
type BulkString string

// Null is the reply for a missing key, $-1.
type Null struct{}

func (SimpleString) reply() {}
func (Error) reply()        {}
func (Integer) reply()      {}
func (BulkString) reply()   {}
func (Null) reply()         {}
