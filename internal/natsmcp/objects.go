package natsmcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// The files of attachments: put into the object store by the sender, fetched by the receiver into a folder of its own
// (docs/plan/nats-objekte.md §6). The content never goes into a result of a tool: the agent gets a path.

const (
	objectTimeout   = 60 * time.Second
	maxAttachDirMiB = 64
)

// HasObjects says whether the group has an object store (NATS_OBJECTS): the server of the group was made with one, and
// mcp.env says the size limit.
func (s *Service) HasObjects() bool { return s.cfg.ObjectsFileMax > 0 }

//nolint:ireturn // the library gives the store as an interface
func (s *Service) objectStore(ctx context.Context) (jetstream.ObjectStore, error) {
	if !s.HasObjects() {
		return nil, errors.New("the group has no object store (NATS_OBJECTS): attachments are not possible")
	}
	if !s.Connected() {
		return nil, errors.New("not connected to the NATS server (" + s.connectionState() + ")")
	}
	js, err := jetstream.New(s.nc)
	if err != nil {
		return nil, err
	}
	ob, err := js.ObjectStore(ctx, BucketName(s.cfg.Names.Group))
	if err != nil {
		return nil, fmt.Errorf("the object store of the group: %w", err)
	}
	return ob, nil
}

// newObjectID is the random name of an object.
func newObjectID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system has no randomness
	}
	return hex.EncodeToString(b)
}

// readAttachmentFile reads a file of this machine for sending: below the home directory (after links are followed), a regular
// file, not larger than the limit.
func (s *Service) readAttachmentFile(path string) ([]byte, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(home, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, "", fmt.Errorf("the attachment %s: %w", filepath.Base(path), errors.Unwrap(err))
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return nil, "", err
	}
	if rel, err := filepath.Rel(realHome, resolved); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, "", errors.New("the attachment has to be a file in your home directory")
	}
	f, err := os.Open(resolved)
	if err != nil {
		return nil, "", fmt.Errorf("the attachment %s: %w", filepath.Base(resolved), errors.Unwrap(err))
	}
	defer func() { _ = f.Close() }() // read only
	st, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	if !st.Mode().IsRegular() {
		return nil, "", errors.New("the attachment is no regular file")
	}
	if st.Size() < 1 || st.Size() > s.cfg.ObjectsFileMax {
		return nil, "", fmt.Errorf("the attachment has %d bytes, at least 1 and at most %d are allowed", st.Size(), s.cfg.ObjectsFileMax)
	}
	b, err := io.ReadAll(io.LimitReader(f, s.cfg.ObjectsFileMax+1))
	if err != nil {
		return nil, "", err
	}
	return b, CleanName(filepath.Base(resolved)), nil
}

// PutAttachment puts a file of this machine into the object store and returns the reference for the message.
func (s *Service) PutAttachment(ctx context.Context, path string) (*Attachment, error) {
	ctx, cancel := context.WithTimeout(ctx, objectTimeout)
	defer cancel()
	ob, err := s.objectStore(ctx)
	if err != nil {
		return nil, err
	}
	data, name, err := s.readAttachmentFile(path)
	if err != nil {
		return nil, err
	}
	return putData(ctx, ob, data, name)
}

// PutData puts data that is not a file of this machine (the file of viper nats, which came over the connection) into the object
// store, after the same checks, and returns the reference.
func (s *Service) PutData(ctx context.Context, data []byte, name string) (*Attachment, error) {
	ctx, cancel := context.WithTimeout(ctx, objectTimeout)
	defer cancel()
	ob, err := s.objectStore(ctx)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) < 1 || int64(len(data)) > s.cfg.ObjectsFileMax {
		return nil, fmt.Errorf("the attachment has %d bytes, at least 1 and at most %d are allowed", len(data), s.cfg.ObjectsFileMax)
	}
	return putData(ctx, ob, data, CleanName(name))
}

func putData(ctx context.Context, ob jetstream.ObjectStore, data []byte, name string) (*Attachment, error) {
	if err := CheckAttachment("", data); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	att := &Attachment{ID: newObjectID(), Type: Classify(data), Name: name, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}
	if _, err := ob.PutBytes(ctx, att.ID, data); err != nil {
		if strings.Contains(err.Error(), "maximum bytes exceeded") || strings.Contains(err.Error(), "10077") {
			return nil, errors.New("the object store of the group is full: files go when their time is up (NATS_OBJECTS_TTL)")
		}
		return nil, fmt.Errorf("putting the file into the object store: %w", err)
	}
	return att, nil
}

// FetchTo fetches an object by its ID and the checksum the user read from the message (viper nats listen shows it) into a folder
// and returns the path. Size and type are not known beforehand: the type is read from the data, and a ZIP file is looked at.
func (s *Service) FetchTo(ctx context.Context, id, sha, dir string) (string, error) {
	if !attIDRe.MatchString(id) || !sha256Re.MatchString(sha) {
		return "", errors.New("the ID is 32 and the checksum 64 hexadecimal characters")
	}
	ctx, cancel := context.WithTimeout(ctx, objectTimeout)
	defer cancel()
	ob, err := s.objectStore(ctx)
	if err != nil {
		return "", err
	}
	info, err := ob.GetInfo(ctx, id)
	if err != nil {
		return "", fetchError(err)
	}
	if info.Size > uint64(s.cfg.ObjectsFileMax) {
		return "", fmt.Errorf("the file has %d bytes, at most %d are allowed", info.Size, s.cfg.ObjectsFileMax)
	}
	data, err := ob.GetBytes(ctx, id)
	if err != nil {
		return "", fetchError(err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != sha {
		return "", errors.New("the file does not match the checksum: it was changed, or it is not the file of the message")
	}
	if err := CheckAttachment("", data); err != nil {
		return "", err
	}
	att := &Attachment{ID: id, Type: Classify(data), Size: int64(len(data)), SHA256: sha}
	return saveAttachment(dir, false, att, data)
}

// FetchAttachment fetches the file of a reference into the folder of attachments, checks it against the reference (size,
// checksum, type, and for a ZIP file its contents) and returns its path. It is the reference of a message from a machine of the
// group that is fetched, never an ID the agent made up.
func (s *Service) FetchAttachment(ctx context.Context, att *Attachment) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, objectTimeout)
	defer cancel()
	ob, err := s.objectStore(ctx)
	if err != nil {
		return "", err
	}
	info, err := ob.GetInfo(ctx, att.ID)
	if err != nil {
		return "", fetchError(err)
	}
	if info.Size != uint64(att.Size) || att.Size > s.cfg.ObjectsFileMax {
		return "", fmt.Errorf("the file is not the one the message names (%d bytes, the message says %d, at most %d are allowed)", info.Size, att.Size, s.cfg.ObjectsFileMax)
	}
	data, err := ob.GetBytes(ctx, att.ID)
	if err != nil {
		return "", fetchError(err)
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != att.Size || hex.EncodeToString(sum[:]) != att.SHA256 {
		return "", errors.New("the file does not match the checksum of the message: it was changed, or it is not the file of the message")
	}
	if err := CheckAttachment(att.Type, data); err != nil {
		return "", err
	}
	return saveAttachment(s.cfg.AttachDir, true, att, data)
}

// saveAttachment writes a file that was checked into the folder for attachments, under a name made of its ID and the ending of its
// type, and keeps the folder small.
// prune is true for the folder of attachments of a machine, which viper keeps small; the folder a user names is never pruned.
func saveAttachment(dir string, prune bool, att *Attachment, data []byte) (string, error) {
	if dir == "" {
		return "", errors.New("no folder for attachments")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, att.ID+Extension(att.Type))
	tmp, err := os.CreateTemp(dir, "fetch-*.tmp")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone after the rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	if prune {
		pruneAttachments(dir, path)
	}
	return path, nil
}

func fetchError(err error) error {
	switch {
	case errors.Is(err, jetstream.ErrObjectNotFound):
		return errors.New("the file is not in the object store (its time is up, or it was removed)")
	case strings.Contains(err.Error(), "digests do not match"):
		return errors.New("the file in the object store is damaged (its time is up, or it was changed)")
	}
	return fmt.Errorf("fetching the file: %w", err)
}

// pruneAttachments keeps the folder small: the oldest files go until at most maxAttachDirLen files and maxAttachDirMiB MiB are
// left; the file just made stays.
func pruneAttachments(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type file struct {
		path string
		at   time.Time
		size int64
	}
	var files []file
	var total int64
	for _, e := range entries {
		st, err := e.Info()
		if err != nil || !st.Mode().IsRegular() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		files = append(files, file{filepath.Join(dir, e.Name()), st.ModTime(), st.Size()})
		total += st.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].at.Before(files[j].at) })
	for _, f := range files {
		if len(files) <= maxAttachDirLen && total <= maxAttachDirMiB<<20 {
			return
		}
		if f.path == keep {
			continue
		}
		if os.Remove(f.path) == nil {
			files, total = files[1:], total-f.size
		}
	}
}
