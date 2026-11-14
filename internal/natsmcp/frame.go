package natsmcp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// A command of viper nats that sends a file has the text and the file on standard input, in one stream (the text and the file
// are no words of the command, so nothing of them is quoted for a shell): a line "viper-attach <text bytes> <file bytes>
// <name>", then the text, then the file, then nothing.

const frameWord = "viper-attach"

// Framed is the file of a frame.
type Framed struct {
	Name string
	Data []byte
}

// AttachFrame is what viper writes to the standard input of the command on the machine.
func AttachFrame(text, name string, data []byte) string {
	return fmt.Sprintf("%s %d %d %s\n%s%s", frameWord, len(text), len(data), CleanName(name), text, data)
}

// ReadFrame reads a frame. The lengths are checked against the limits before anything is read, and nothing may follow the file.
func ReadFrame(in io.Reader, maxText int, maxFile int64) (text string, file *Framed, err error) {
	r := bufio.NewReader(io.LimitReader(in, int64(maxText)+maxFile+512))
	head, err := r.ReadString('\n')
	if err != nil {
		return "", nil, errors.New("no header of the file on the standard input")
	}
	f := strings.SplitN(strings.TrimSuffix(head, "\n"), " ", 4)
	if len(f) != 4 || f[0] != frameWord {
		return "", nil, errors.New("the standard input does not start with the header of a file")
	}
	tl, err1 := strconv.Atoi(f[1])
	fl, err2 := strconv.ParseInt(f[2], 10, 64)
	switch {
	case err1 != nil || err2 != nil || tl < 0 || fl < 1:
		return "", nil, errors.New("the header of the file has bad sizes")
	case tl > maxText:
		return "", nil, fmt.Errorf("the text has %d bytes, at most %d are allowed", tl, maxText)
	case fl > maxFile:
		return "", nil, fmt.Errorf("the file has %d bytes, at most %d are allowed (the group has no object store, or the file is larger than NATS_OBJECTS_FILE_MAX)", fl, maxFile)
	}
	tb := make([]byte, tl)
	if _, err := io.ReadFull(r, tb); err != nil {
		return "", nil, errors.New("the standard input ends in the text")
	}
	data := make([]byte, fl)
	if _, err := io.ReadFull(r, data); err != nil {
		return "", nil, errors.New("the standard input ends in the file")
	}
	if _, err := r.ReadByte(); !errors.Is(err, io.EOF) {
		return "", nil, errors.New("there is more on the standard input than the header says")
	}
	return string(tb), &Framed{Name: CleanName(f[3]), Data: data}, nil
}
