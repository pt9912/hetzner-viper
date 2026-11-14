package natsmcp

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
)

// The object store of a group (docs/plan/nats-objekte.md): one bucket, made by viper with the entry AdminName, whose key stays
// on the machine of the server. The machines only put and get objects (natsserver.objectRights).

// AdminName is the name of the entry that makes the bucket (natsserver.AdminName).
const AdminName = "viper-admin"

// BucketName is the bucket of a group (natsserver.ObjectBucket).
func BucketName(group string) string { return "viper-" + group }

func streamName(group string) string { return "OBJ_" + BucketName(group) }

// maxConsumers bounds the consumers of the stream: a machine may create consumers (a read does), and without a bound one machine
// could make as many as it likes. The ordered consumers of a read are deleted when it is done.
const maxConsumers = 64

// maxMsgSize is the largest message of the stream: a chunk of an object is 128 KiB, with its headers a little more.
const maxMsgSize = 256 << 10

// BucketSpec is what the bucket of a group has to be.
type BucketSpec struct {
	Group    string
	MaxBytes int64         // the whole store
	TTL      time.Duration // how long an object lives
}

// maxMsgs bounds the number of messages (chunks and metadata): a flood of tiny objects fills the stream by messages long
// before it fills it by bytes.
func (b BucketSpec) maxMsgs() int64 {
	if n := b.MaxBytes / 16384; n > 1024 {
		return n
	}
	return 1024
}

// BucketInfo is the configuration of the stream of the bucket as the server has it.
type BucketInfo struct {
	MaxBytes     int64
	MaxAge       time.Duration
	MaxMsgs      int64
	MaxMsgSize   int32
	MaxConsumers int
	Storage      string
	Replicas     int
	Discard      string
}

// Lines are the values as key=value lines, for viper check.
func (i BucketInfo) Lines() string {
	return fmt.Sprintf("bucket_max_bytes=%d\nbucket_max_age=%d\nbucket_max_msgs=%d\nbucket_max_msg_size=%d\nbucket_max_consumers=%d\nbucket_storage=%s\nbucket_replicas=%d\nbucket_discard=%s\n",
		i.MaxBytes, int64(i.MaxAge/time.Second), i.MaxMsgs, i.MaxMsgSize, i.MaxConsumers, i.Storage, i.Replicas, i.Discard)
}

func infoOf(c jetstream.StreamConfig) BucketInfo {
	storage := "file"
	if c.Storage == jetstream.MemoryStorage {
		storage = "memory"
	}
	discard := "old"
	if c.Discard == jetstream.DiscardNew {
		discard = "new"
	}
	return BucketInfo{MaxBytes: c.MaxBytes, MaxAge: c.MaxAge, MaxMsgs: c.MaxMsgs, MaxMsgSize: c.MaxMsgSize, MaxConsumers: c.MaxConsumers, Storage: storage, Replicas: c.Replicas, Discard: discard}
}

// Differences says in what the bucket differs from the spec, in words ("" lines for none).
func (b BucketSpec) Differences(i BucketInfo) []string {
	var d []string
	if i.MaxBytes != b.MaxBytes {
		d = append(d, fmt.Sprintf("max_bytes is %d, should be %d", i.MaxBytes, b.MaxBytes))
	}
	if i.MaxAge != b.TTL {
		d = append(d, fmt.Sprintf("max_age is %s, should be %s", i.MaxAge, b.TTL))
	}
	if i.MaxMsgs != b.maxMsgs() {
		d = append(d, fmt.Sprintf("max_msgs is %d, should be %d", i.MaxMsgs, b.maxMsgs()))
	}
	if i.MaxMsgSize != maxMsgSize {
		d = append(d, fmt.Sprintf("max_msg_size is %d, should be %d", i.MaxMsgSize, maxMsgSize))
	}
	if i.MaxConsumers != maxConsumers {
		d = append(d, fmt.Sprintf("max_consumers is %d, should be %d", i.MaxConsumers, maxConsumers))
	}
	if i.Storage != "file" || i.Replicas != 1 {
		d = append(d, fmt.Sprintf("storage is %s with %d replica(s), should be file with 1", i.Storage, i.Replicas))
	}
	if i.Discard != "new" {
		d = append(d, "discard is "+i.Discard+", should be new: with old the chunks of an object go before its metadata")
	}
	return d
}

// EnsureBucket makes the bucket of the group if it is not there. A bucket that is there is compared with the spec and left
// alone: what someone changed by hand is reported, not overwritten.
func EnsureBucket(ctx context.Context, nc *nats.Conn, spec BucketSpec) (created bool, diffs []string, err error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return false, nil, err
	}
	if info, err := bucketInfo(ctx, js, spec.Group); err != nil {
		return false, nil, err
	} else if info != nil {
		return false, spec.Differences(*info), nil
	}
	if _, err := js.CreateObjectStore(ctx, jetstream.ObjectStoreConfig{
		Bucket: BucketName(spec.Group), Description: "viper: attachments of the group " + spec.Group,
		Storage: jetstream.FileStorage, Replicas: 1, TTL: spec.TTL, MaxBytes: spec.MaxBytes,
	}); err != nil {
		return false, nil, fmt.Errorf("making the bucket: %w", err)
	}
	// the object store takes neither a limit for the messages nor one for their size: the stream is changed afterwards
	s, err := js.Stream(ctx, streamName(spec.Group))
	if err != nil {
		return true, nil, fmt.Errorf("reading the stream: %w", err)
	}
	cfg := s.CachedInfo().Config
	cfg.MaxMsgs, cfg.MaxMsgSize, cfg.MaxConsumers = spec.maxMsgs(), maxMsgSize, maxConsumers
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		return true, nil, fmt.Errorf("limiting the stream: %w", err)
	}
	return true, nil, nil
}

// ReadBucket reads the configuration of the bucket; nil if there is none.
func ReadBucket(ctx context.Context, nc *nats.Conn, group string) (*BucketInfo, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}
	return bucketInfo(ctx, js, group)
}

func bucketInfo(ctx context.Context, js jetstream.JetStream, group string) (*BucketInfo, error) {
	s, err := js.Stream(ctx, streamName(group))
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	i := infoOf(s.CachedInfo().Config)
	return &i, nil
}

// ConnectAdmin connects as the entry that makes the bucket: its seed is in the directory of the server, the inbox prefix is its
// own (the entry may read nothing else).
func ConnectAdmin(cfg Config, seedFile string) (*nats.Conn, error) {
	seed, err := os.ReadFile(seedFile)
	if err != nil {
		return nil, fmt.Errorf("reading the key: %w", err)
	}
	kp, err := nkeys.FromSeed([]byte(strings.TrimSpace(string(seed))))
	if err != nil {
		return nil, fmt.Errorf("the key file holds no NKey seed: %w", err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		return nil, err
	}
	opts := []nats.Option{nats.Name("viper-admin"), nats.Nkey(pub, kp.Sign), nats.CustomInboxPrefix("_INBOX." + AdminName), nats.Timeout(10 * time.Second), nats.NoEcho()}
	if cfg.CAFile != "" {
		o, err := tlsOption(cfg)
		if err != nil {
			return nil, err
		}
		opts = append(opts, o)
	}
	return nats.Connect(cfg.URL, opts...)
}

// BucketMain runs ensure-bucket and bucket-info: the commands viper runs on the machine of the server over ssh. The key of
// the entry is admin.nk next to connection.env.
func BucketMain(name string, cfg Config, dir string, args []string, stdout, stderr io.Writer) int {
	spec := BucketSpec{Group: cfg.Names.Group}
	var days int64
	fs := flag.NewFlagSet("viper-nats-mcp "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	if name == "ensure-bucket" {
		fs.Int64Var(&spec.MaxBytes, "max-bytes", 0, "the size of the whole store")
		fs.Int64Var(&days, "ttl-days", 0, "how long an object lives")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	spec.TTL = time.Duration(days) * 24 * time.Hour
	if name == "ensure-bucket" && (spec.MaxBytes < 1<<20 || days < 1) {
		fmt.Fprintln(stderr, "viper-nats-mcp ensure-bucket: --max-bytes and --ttl-days are needed")
		return 2
	}
	nc, err := ConnectAdmin(cfg, dir+"/admin.nk")
	if err != nil {
		fmt.Fprintln(stderr, "nats bucket:", err)
		return 1
	}
	defer nc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if name == "bucket-info" {
		info, err := ReadBucket(ctx, nc, spec.Group)
		switch {
		case err != nil:
			fmt.Fprintln(stderr, "nats bucket:", err)
			return 1
		case info == nil:
			fmt.Fprintln(stdout, "bucket=missing")
		default:
			fmt.Fprint(stdout, "bucket=ok\n"+info.Lines())
		}
		return 0
	}
	created, diffs, err := EnsureBucket(ctx, nc, spec)
	switch {
	case err != nil:
		fmt.Fprintln(stderr, "nats bucket:", err)
		return 1
	case created:
		fmt.Fprintln(stdout, "nats bucket: made "+BucketName(spec.Group))
	case len(diffs) > 0:
		fmt.Fprintln(stdout, "nats bucket: differs from the settings (left as it is): "+strings.Join(diffs, "; "))
	default:
		fmt.Fprintln(stdout, "nats bucket: "+BucketName(spec.Group)+" is as the settings say")
	}
	return 0
}
