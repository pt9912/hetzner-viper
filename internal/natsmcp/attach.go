package natsmcp

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"unicode/utf8"
)

// Attachments (docs/plan/nats-objekte.md): an image, a PDF or a ZIP file in the object store of the group, named in a message by
// a reference. The type is read from the first bytes and nothing else; a ZIP file is looked at (its directory and, once, all its
// data into nowhere) and never unpacked.

// The types of an attachment.
const (
	TypePDF  = "pdf"
	TypeZIP  = "zip"
	TypePNG  = "png"
	TypeJPEG = "jpeg"
	TypeGIF  = "gif"
	TypeWebP = "webp"
)

// Attachment is the reference to a file in the object store. The sender chooses ID (the name of the object), the server proves
// who the sender is; the checksum is what the receiver checks the file against.
type Attachment struct {
	ID     string `json:"id"`     // 32 hexadecimal characters
	Type   string `json:"type"`   // one of the types above
	Name   string `json:"name"`   // for display only, never a path
	Size   int64  `json:"size"`   // bytes
	SHA256 string `json:"sha256"` // of the content
}

// The limits of what is looked at.
const (
	maxAttachment   = 64 << 20 // the largest file at all (NATS_OBJECTS_FILE_MAX is at most this)
	maxZipEntries   = 1000
	maxZipExpanded  = 64 << 20
	maxZipRatio     = 100
	maxAttachName   = 64
	maxAttachDirLen = 50
)

// Classify says what type of attachment the data is, from its first bytes ("" if it is none). A PDF has to begin with its mark,
// a ZIP file with the header of its first entry: data in front of them (a program, junk) is no attachment.
func Classify(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return TypePNG
	case bytes.HasPrefix(b, []byte("\xff\xd8\xff")):
		return TypeJPEG
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return TypeGIF
	case len(b) >= 12 && bytes.HasPrefix(b, []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return TypeWebP
	case bytes.HasPrefix(b, []byte("%PDF-")):
		return TypePDF
	case bytes.HasPrefix(b, []byte("PK\x03\x04")):
		return TypeZIP
	}
	return ""
}

// Extension is the file name ending of a type; fixed, so nothing the sender says reaches a file name.
func Extension(typ string) string {
	switch typ {
	case TypePDF:
		return ".pdf"
	case TypeZIP:
		return ".zip"
	case TypePNG:
		return ".png"
	case TypeJPEG:
		return ".jpg"
	case TypeGIF:
		return ".gif"
	case TypeWebP:
		return ".webp"
	}
	return ""
}

// validType says whether t is one of the types.
func validType(t string) bool { return Extension(t) != "" }

// CleanName makes a name for display of a path or of what the sender says: the last part, no control characters, no
// separators, at most maxAttachName bytes, valid UTF-8. Nothing usable is left as "file".
func CleanName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		if !nameRune(r) {
			continue
		}
		if b.Len()+utf8.RuneLen(r) > maxAttachName {
			break
		}
		b.WriteRune(r)
	}
	if s := strings.TrimSpace(b.String()); s != "" && s != "." && s != ".." {
		return s
	}
	return "file"
}

// nameRune says whether a character may be in a name for display: no control characters, no characters that end up in a path or in
// the markup of the inbox.
func nameRune(r rune) bool {
	switch {
	case r < 0x20 || r == 0x7f || r == utf8.RuneError || (r >= 0x80 && r < 0xa0):
		return false
	case strings.ContainsRune(":|<>", r):
		return false
	}
	return true
}

// validAttachName says whether a name of an attachment in a message is as CleanName makes it.
func validAttachName(name string) bool { return CleanName(name) == name }

// CheckZIP looks at a ZIP file without unpacking it: first its directory, then, once, all of its data read into nowhere. The
// directory can lie about the sizes (a few KB can say 10 bytes and hold 200 MiB), so only the second look counts for the size.
func CheckZIP(b []byte) error {
	r, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return fmt.Errorf("not a usable ZIP file: %w", err)
	}
	if len(r.File) == 0 {
		return errors.New("the ZIP file has no entries")
	}
	if len(r.File) > maxZipEntries {
		return fmt.Errorf("the ZIP file has %d entries, at most %d are allowed", len(r.File), maxZipEntries)
	}
	if err := checkZIPEntries(r.File); err != nil {
		return err
	}
	return readZIPInto(r.File, int64(len(b)))
}

func checkZIPEntries(files []*zip.File) error {
	seen := map[string]bool{}
	type span struct{ from, to int64 }
	var spans []span
	for _, f := range files {
		if err := checkZIPName(f.Name); err != nil {
			return err
		}
		key := strings.ToLower(strings.TrimSuffix(f.Name, "/"))
		if seen[key] {
			return fmt.Errorf("the ZIP file has the name %q twice", truncate(f.Name))
		}
		seen[key] = true
		if f.Mode()&fs.ModeSymlink != 0 { // a link (a ZIP file made on Unix says so in its mode bits; one made elsewhere cannot)
			return fmt.Errorf("the ZIP file has a link: %q", truncate(f.Name))
		}
		if f.Flags&0x1 != 0 || f.Flags&0x40 != 0 || f.Method == 99 {
			return fmt.Errorf("the ZIP file has an encrypted entry: %q", truncate(f.Name))
		}
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return fmt.Errorf("the ZIP file has an entry with the method %d: %q", f.Method, truncate(f.Name))
		}
		off, err := f.DataOffset()
		if err != nil {
			return fmt.Errorf("the ZIP file has a damaged entry: %q", truncate(f.Name))
		}
		spans = append(spans, span{off, off + int64(f.CompressedSize64)})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].from < spans[j].from })
	for i := 1; i < len(spans); i++ {
		if spans[i].from < spans[i-1].to {
			return errors.New("the ZIP file has entries that overlap")
		}
	}
	return nil
}

// checkZIPName refuses what could leave the folder it is unpacked into, and what makes names ambiguous.
func checkZIPName(name string) error {
	switch {
	case name == "" || len(name) > 512:
		return errors.New("the ZIP file has an entry with no name or a very long one")
	case !utf8.ValidString(name):
		return errors.New("the ZIP file has a name that is no text")
	case strings.HasPrefix(name, "/") || strings.Contains(name, `\`) || strings.Contains(name, "//") || (len(name) >= 2 && name[1] == ':'):
		return fmt.Errorf("the ZIP file has a name that is a path outside of a folder: %q", truncate(name))
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("the ZIP file has a name with a control character: %q", truncate(name))
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return fmt.Errorf("the ZIP file has a name that climbs out of its folder: %q", truncate(name))
		}
	}
	return nil
}

// readZIPInto reads every entry once into nowhere; Go checks the size and the checksum of each. The data may be at most
// maxZipExpanded and maxZipRatio times the size of the file.
func readZIPInto(files []*zip.File, packed int64) error {
	limit := int64(maxZipExpanded)
	if r := packed * maxZipRatio; r < limit {
		limit = r
	}
	var total int64
	for _, f := range files {
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("an entry of the ZIP file cannot be read: %q", truncate(f.Name))
		}
		n, err := io.Copy(io.Discard, io.LimitReader(rc, limit-total+1))
		_ = rc.Close()
		total += n
		if total > limit {
			return fmt.Errorf("the ZIP file holds more than %d MiB once unpacked or more than %d times its own size", maxZipExpanded>>20, maxZipRatio)
		}
		if err != nil {
			return fmt.Errorf("an entry of the ZIP file is damaged: %q", truncate(f.Name))
		}
	}
	return nil
}

// CheckAttachment is what is checked of the data of an attachment, by the sender and by the receiver: the type from the first
// bytes (it has to be the one named), and for a ZIP file its contents.
func CheckAttachment(typ string, data []byte) error {
	got := Classify(data)
	if got == "" {
		return errors.New("the file is no image (PNG, JPEG, GIF, WebP), PDF or ZIP file: its first bytes are none of theirs")
	}
	if typ != "" && got != typ {
		return fmt.Errorf("the file is a %s, not a %s", got, typ)
	}
	if got == TypeZIP {
		return CheckZIP(data)
	}
	return nil
}
