package natsmcp_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/natsmcp"
)

// png is the smallest thing Classify takes for a PNG file: its eight first bytes, then anything.
func png(extra int) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, extra)...)
}

func makeZIP(t *testing.T, entries map[string][]byte, mod func(*zip.FileHeader)) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	// a fixed order
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	for _, n := range names {
		h := &zip.FileHeader{Name: n, Method: zip.Deflate}
		if mod != nil {
			mod(h)
		}
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(entries[n]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestTheTypeOfAnAttachmentComesFromItsFirstBytesAndNothingElse(t *testing.T) {
	good := makeZIP(t, map[string][]byte{"a.txt": []byte("hello")}, nil)
	for name, tc := range map[string]struct {
		data []byte
		want string
	}{
		"png":                                 {png(10), natsmcp.TypePNG},
		"jpeg":                                {[]byte("\xff\xd8\xff\xe0abc"), natsmcp.TypeJPEG},
		"gif87":                               {[]byte("GIF87a...."), natsmcp.TypeGIF},
		"gif89":                               {[]byte("GIF89a...."), natsmcp.TypeGIF},
		"webp":                                {[]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), natsmcp.TypeWebP},
		"wav is no webp":                      {[]byte("RIFF\x00\x00\x00\x00WAVEfmt "), ""},
		"pdf":                                 {[]byte("%PDF-1.7\n..."), natsmcp.TypePDF},
		"pdf with junk before":                {[]byte("\n\n%PDF-1.4\n"), ""},
		"zip":                                 {good, natsmcp.TypeZIP},
		"empty zip":                           {[]byte("PK\x05\x06" + strings.Repeat("\x00", 18)), ""},
		"program before the zip":              {append([]byte("MZ\x90\x00"), good...), ""},
		"text":                                {[]byte("hello"), ""},
		"nothing":                             {nil, ""},
		"a pdf with a zip behind it is a pdf": {append([]byte("%PDF-1.4\n"), good...), natsmcp.TypePDF},
	} {
		if got := natsmcp.Classify(tc.data); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
	if err := natsmcp.CheckAttachment(natsmcp.TypePDF, png(5)); err == nil {
		t.Error("a PNG that is called a PDF was accepted")
	}
	if err := natsmcp.CheckAttachment("", []byte("just text")); err == nil {
		t.Error("text was accepted as an attachment")
	}
}

func TestANameForDisplayHasNoPathAndNoControlCharacters(t *testing.T) {
	for in, want := range map[string]string{
		"report.pdf":             "report.pdf",
		"/home/x/../etc/passwd":  "passwd",
		`C:\Users\x\evil.zip`:    "evil.zip",
		"a\nb|c<d>:e":            "abcde",
		"":                       "file",
		"..":                     "file",
		"\x01\x02":               "file",
		strings.Repeat("x", 200): strings.Repeat("x", 64),
	} {
		if got := natsmcp.CleanName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestAZipFileIsLookedAtAndNeverUnpacked(t *testing.T) {
	ok := makeZIP(t, map[string][]byte{"a.txt": []byte("hello"), "dir/b.txt": []byte("world")}, nil)
	if err := natsmcp.CheckZIP(ok); err != nil {
		t.Fatalf("a good zip file: %v", err)
	}
	bad := map[string]map[string][]byte{
		"a name that climbs out":     {"../x": []byte("x")},
		"in the middle":              {"a/../../x": []byte("x")},
		"an absolute path":           {"/etc/passwd": []byte("x")},
		"a drive letter":             {"C:/x": []byte("x")},
		"a drive letter, no slash":   {"C:x": []byte("x")},
		"a backslash":                {`a\b`: []byte("x")},
		"two slashes":                {"a//b": []byte("x")},
		"a control character":        {"a\nb": []byte("x")},
		"the same name in two cases": {"A.txt": []byte("1"), "a.TXT": []byte("2")},
	}
	for name, entries := range bad {
		if err := natsmcp.CheckZIP(makeZIP(t, entries, nil)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	link := makeZIP(t, map[string][]byte{"l": []byte("/etc/passwd")}, func(h *zip.FileHeader) { h.SetMode(fs.ModeSymlink | 0o777) })
	if err := natsmcp.CheckZIP(link); err == nil {
		t.Error("a link was accepted")
	}
	enc := makeZIP(t, map[string][]byte{"e": []byte("x")}, func(h *zip.FileHeader) { h.Flags |= 0x1 })
	if err := natsmcp.CheckZIP(enc); err == nil {
		t.Error("an encrypted entry was accepted")
	}
	many := map[string][]byte{}
	for i := 0; i < 1001; i++ {
		many[strings.Repeat("a", 1)+string(rune('A'+i%26))+strings.Repeat("b", i/26)] = []byte("x")
	}
	if err := natsmcp.CheckZIP(makeZIP(t, many, nil)); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Errorf("too many entries: %v", err)
	}
	if err := natsmcp.CheckZIP(makeZIP(t, nil, nil)); err == nil {
		t.Error("an empty archive was accepted")
	}
}

// The directory of a ZIP file can say what it likes about the size: only reading it tells.
func TestAZipBombIsNoMatterWhatItsDirectorySays(t *testing.T) {
	bomb := makeZIP(t, map[string][]byte{"zeros": make([]byte, 70<<20)}, nil)
	if len(bomb) > 1<<20 {
		t.Fatalf("the bomb is not small: %d", len(bomb))
	}
	if err := natsmcp.CheckZIP(bomb); err == nil || !strings.Contains(err.Error(), "once unpacked") {
		t.Errorf("honest bomb: %v", err)
	}
	// the same with a directory that lies: the uncompressed size is patched to 10 in the central directory and the local header
	forged := append([]byte(nil), bomb...)
	if i := bytes.LastIndex(forged, []byte("PK\x01\x02")); i >= 0 {
		binary.LittleEndian.PutUint32(forged[i+24:], 10)
	}
	if i := bytes.Index(forged, []byte("PK\x03\x04")); i >= 0 {
		binary.LittleEndian.PutUint32(forged[i+22:], 10)
	}
	if err := natsmcp.CheckZIP(forged); err == nil {
		t.Error("a bomb with a forged directory was accepted")
	}
	// a small file that is fine for the ratio but not a bomb
	if err := natsmcp.CheckZIP(makeZIP(t, map[string][]byte{"x": bytes.Repeat([]byte("hello "), 1000)}, nil)); err != nil {
		t.Errorf("text that packs well: %v", err)
	}
}

func TestZipEntriesThatOverlapAreRefused(t *testing.T) {
	z := makeZIP(t, map[string][]byte{"a": []byte("aaaaaaaaaaaaaaaa"), "b": []byte("bbbbbbbbbbbbbbbb")}, func(h *zip.FileHeader) { h.Method = zip.Store })
	// the second entry of the central directory points at the first local header
	first := bytes.Index(z, []byte("PK\x01\x02"))
	second := first + bytes.Index(z[first+4:], []byte("PK\x01\x02")) + 4
	binary.LittleEndian.PutUint32(z[second+42:], 0)
	if err := natsmcp.CheckZIP(z); err == nil {
		t.Error("overlapping entries were accepted")
	}
}

func TestAnAttachmentInAMessageIsReadStrictly(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	sum := strings.Repeat("a", 64)
	att := func(extra string) string {
		return `{"id":"` + id + `","type":"pdf","name":"r.pdf","size":10,"sha256":"` + sum + `"` + extra + `}`
	}
	msg := func(kind, a string) []byte {
		return []byte(`{"v":1,"kind":"` + kind + `","id":"` + id + `","text":"x","att":` + a + `}`)
	}
	if e, err := natsmcp.Decode(msg("msg", att("")), natsmcp.DefaultMaxText); err != nil || e.Att == nil || e.Att.Type != "pdf" || e.Att.Size != 10 {
		t.Fatalf("%+v %v", e, err)
	}
	for name, data := range map[string][]byte{
		"an unknown field":      msg("msg", att(`,"url":"x"`)),
		"a field twice":         msg("msg", `{"id":"`+id+`","id":"`+id+`","type":"pdf","name":"r","size":10,"sha256":"`+sum+`"}`),
		"a bad id":              msg("msg", strings.Replace(att(""), id, "xyz", 1)),
		"a bad type":            msg("msg", strings.Replace(att(""), `"pdf"`, `"exe"`, 1)),
		"a path as the name":    msg("msg", strings.Replace(att(""), "r.pdf", "../r.pdf", 1)),
		"no size":               msg("msg", strings.Replace(att(""), `"size":10`, `"size":0`, 1)),
		"too large":             msg("msg", strings.Replace(att(""), `"size":10`, `"size":99999999999`, 1)),
		"a bad checksum":        msg("msg", strings.Replace(att(""), sum, "abc", 1)),
		"on an answer":          msg("res", att("")),
		"on a presence":         []byte(`{"v":1,"kind":"presence","state":"up","att":` + att("") + `}`),
		"att twice":             []byte(`{"v":1,"kind":"msg","text":"x","att":` + att("") + `,"att":` + att("") + `}`),
		"att that is no object": []byte(`{"v":1,"kind":"msg","text":"x","att":"file"}`),
	} {
		if _, err := natsmcp.Decode(data, natsmcp.DefaultMaxText); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if e, err := natsmcp.Decode([]byte(`{"v":1,"kind":"msg","text":"x"}`), natsmcp.DefaultMaxText); err != nil || e.Att != nil {
		t.Errorf("a message without an attachment: %+v %v", e, err)
	}
}

// --- through a real server with an object store

type objectGroup struct {
	a, b *machine
	home string
	srv  *objectServer
}

func newObjectGroup(t *testing.T, bHasStore bool) objectGroup {
	t.Helper()
	srv := newObjectServer(t, "g")
	if _, _, err := natsmcp.EnsureBucket(ctx5(t), srv.admin(t), testSpec()); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	g := objectGroup{home: home, srv: srv}
	for _, name := range []string{"a", "b"} {
		m := &machine{}
		cfg := natsmcp.Config{URL: srv.url, SeedFile: srv.seed[name], Names: natsmcp.Names{Group: "g", Me: name}, PushFrom: map[string]bool{"a": name == "b"},
			MaxText: natsmcp.DefaultMaxText, PushPerMin: 3, InboxMax: 200, InboxPerMan: 50, Version: "v9.9.9", AttachDir: filepath.Join(home, "att-"+name)}
		if name == "a" || bHasStore {
			cfg.ObjectsFileMax = 4 << 20
		}
		m.svc = natsmcp.NewService(cfg, nopLog{}, func(content string, meta map[string]string) error {
			m.mu.Lock()
			m.pushes = append(m.pushes, pushed{content, meta})
			m.mu.Unlock()
			return nil
		})
		m.svc.SetJitter(func() time.Duration { return 5 * time.Millisecond })
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		if err := m.svc.Connect(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(m.svc.Close)
		if name == "a" {
			g.a = m
		} else {
			g.b = m
		}
	}
	eventually(t, "a knows what b can do", func() bool {
		for _, p := range g.a.svc.PeersTable().Present(time.Now()) {
			if p.Name == "b" && p.CapsKnown {
				return true
			}
		}
		return false
	})
	return g
}

func (g objectGroup) file(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(g.home, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAFileGoesThroughTheObjectStoreAndIsFetchedAndCheckedByTheReceiver(t *testing.T) {
	g := newObjectGroup(t, true)
	data := png(300 << 10)
	out, err := call(t, g.a.svc, "send", map[string]any{"to": "b", "text": "look at this", "attachment": g.file(t, "shot.png", data)})
	if err != nil || !strings.Contains(out, "with the file shot.png (png") {
		t.Fatalf("%q %v", out, err)
	}
	eventually(t, "b has the message", func() bool { return len(g.b.svc.Inbox().Since(0)) == 1 })
	e := g.b.svc.Inbox().Since(0)[0]
	if e.Att == nil || e.Att.Name != "shot.png" || e.Att.Type != "png" || e.Att.Size != int64(len(data)) {
		t.Fatalf("%+v", e)
	}
	inbox, _ := call(t, g.b.svc, "inbox", nil)
	if !strings.Contains(inbox, "[attachment: shot.png, png,") || !strings.Contains(inbox, "fetch it with the tool fetch, handle 1") {
		t.Errorf("the inbox names the file:\n%s", inbox)
	}
	eventually(t, "b is told", func() bool { return len(g.b.pushed()) == 1 })
	if p := g.b.pushed()[0]; !strings.Contains(p.content, "[attachment: shot.png") || p.meta["attachment"] != "png" {
		t.Errorf("%+v", p)
	}
	res, err := call(t, g.b.svc, "fetch", map[string]any{"handle": 1})
	if err != nil || !strings.Contains(res, "Do not run it") {
		t.Fatalf("%q %v", res, err)
	}
	path := filepath.Join(g.home, "att-b", e.Att.ID+".png")
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, data) {
		t.Errorf("the file is where fetch said: %v", err)
	}
	if st, _ := os.Stat(path); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	if strings.Contains(res, string(data[:20])) {
		t.Error("the content is in the result")
	}
	// a broadcast to a topic b receives
	if _, err := call(t, g.b.svc, "subscribe", map[string]any{"topic": "news"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a knows the topic of b", func() bool {
		for _, p := range g.a.svc.PeersTable().Present(time.Now()) {
			if p.Name == "b" && p.TopicsKnown && len(p.Topics) == 1 {
				return true
			}
		}
		return false
	})
	if out, err = call(t, g.a.svc, "broadcast", map[string]any{"topic": "news", "attachment": g.file(t, "doc.pdf", []byte("%PDF-1.4\nx"))}); err != nil || !strings.Contains(out, "doc.pdf (pdf") {
		t.Fatalf("a broadcast with a file and no text: %q %v", out, err)
	}
	eventually(t, "b has the second", func() bool { return len(g.b.svc.Inbox().Since(0)) == 2 })
}

func TestAFileIsRefusedForWhatItIsAndWhereItIs(t *testing.T) {
	g := newObjectGroup(t, true)
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, png(10), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(g.home, "link.png")
	_ = os.Symlink(outside, link)
	for name, tc := range map[string]struct{ path, want string }{
		"not in the home":   {outside, "home directory"},
		"a link out of it":  {link, "home directory"},
		"no such file":      {filepath.Join(g.home, "none.png"), "none.png"},
		"a directory":       {g.home, "regular file"},
		"text":              {g.file(t, "note.txt", []byte("hello")), "first bytes"},
		"too large":         {g.file(t, "big.png", png(5<<20)), "at most"},
		"empty":             {g.file(t, "empty.png", nil), "at least 1"},
		"a zip that climbs": {g.file(t, "evil.zip", makeZIP(t, map[string][]byte{"../x": []byte("x")}, nil)), "climbs out"},
	} {
		if runtime.GOOS == "windows" && name == "a link out of it" {
			continue
		}
		_, err := call(t, g.a.svc, "send", map[string]any{"to": "b", "text": "x", "attachment": tc.path})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if n := len(g.b.svc.Inbox().Since(0)); n != 0 {
		t.Errorf("nothing was sent: %d", n)
	}
}

func TestAFileIsNotSentToAMachineThatDoesNotUnderstandIt(t *testing.T) {
	g := newObjectGroup(t, false) // b has no object store: it says no "att"
	_, err := call(t, g.a.svc, "send", map[string]any{"to": "b", "text": "x", "attachment": g.file(t, "p.png", png(10))})
	if err == nil || !strings.Contains(err.Error(), "b did not say that it understands attachments") {
		t.Errorf("%v", err)
	}
	for _, tl := range g.b.svc.Tools() {
		if tl.Name == "fetch" {
			t.Error("a machine without an object store has no fetch")
		}
		if props, _ := tl.Schema["properties"].(map[string]any); tl.Name == "send" && props["attachment"] != nil {
			t.Error("a machine without an object store has no attachment parameter")
		}
	}
	var withStore bool
	for _, tl := range g.a.svc.Tools() {
		props, _ := tl.Schema["properties"].(map[string]any)
		if tl.Name == "send" && props["attachment"] != nil {
			withStore = true
			if req, _ := tl.Schema["required"].([]string); len(req) != 1 || req[0] != "to" {
				t.Errorf("with a file the text is not needed: %v", req)
			}
		}
	}
	if !withStore {
		t.Error("send has no attachment parameter")
	}
}

// A message that names a file wrongly cannot make the receiver take a wrong file.
func TestTheReceiverChecksTheFileAgainstTheMessage(t *testing.T) {
	g := newObjectGroup(t, true)
	good, err := g.a.svc.PutAttachment(context.Background(), g.file(t, "ok.png", png(1000)))
	if err != nil {
		t.Fatal(err)
	}
	send := func(a natsmcp.Attachment) {
		b, _ := json.Marshal(natsmcp.Envelope{V: 1, Kind: "msg", Text: "x", Att: &a})
		if err := g.a.svc.RawPublish("viper.g.a.out.b", b); err != nil {
			t.Fatal(err)
		}
		_ = g.a.svc.FlushRaw()
	}
	wrongSum := *good
	wrongSum.SHA256 = strings.Repeat("0", 64)
	wrongSize := *good
	wrongSize.Size++
	wrongType := *good
	wrongType.Type = "pdf"
	missing := *good
	missing.ID = strings.Repeat("0", 32)
	for i, tc := range []struct {
		att  natsmcp.Attachment
		want string
	}{
		{wrongSum, "checksum"}, {wrongSize, "not the one the message names"}, {wrongType, "is a png, not a pdf"}, {missing, "not in the object store"},
	} {
		send(tc.att)
		eventually(t, "the message arrived", func() bool { return len(g.b.svc.Inbox().Since(0)) == i+1 })
		_, err := call(t, g.b.svc, "fetch", map[string]any{"handle": i + 1})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.want, err)
		}
	}
	if _, err := call(t, g.b.svc, "fetch", map[string]any{"handle": 99}); err == nil {
		t.Error("an entry that is not there")
	}
	entries, _ := os.ReadDir(filepath.Join(g.home, "att-b"))
	if len(entries) != 0 {
		t.Errorf("nothing wrong is kept: %v", entries)
	}
}

func TestAFileTravelsOnStandardInputInOneFrameWithTheText(t *testing.T) {
	data := []byte("\x89PNG\r\n\x1a\nbinary \x00\n\xff data")
	frame := natsmcp.AttachFrame("the text\nwith lines", "../dir/shot.png", data)
	text, file, err := natsmcp.ReadFrame(strings.NewReader(frame), 100, 1000)
	if err != nil || text != "the text\nwith lines" || file.Name != "shot.png" || !bytes.Equal(file.Data, data) {
		t.Fatalf("%q %+v %v", text, file, err)
	}
	if text, _, err := natsmcp.ReadFrame(strings.NewReader(natsmcp.AttachFrame("", "a.png", data)), 100, 1000); err != nil || text != "" {
		t.Errorf("a file without text: %q %v", text, err)
	}
	for name, in := range map[string]string{
		"no header":         "",
		"another header":    "hello 1 1 x\nab",
		"bad sizes":         "viper-attach x 1 a.png\nab",
		"no file":           "viper-attach 0 0 a.png\n",
		"a text too long":   "viper-attach 101 4 a.png\n" + strings.Repeat("t", 101) + "abcd",
		"a file too large":  "viper-attach 0 1001 a.png\n" + strings.Repeat("f", 1001),
		"the file is short": "viper-attach 0 10 a.png\nabc",
		"the text is short": "viper-attach 5 3 a.png\nab",
		"more than it says": "viper-attach 0 3 a.png\nabcd",
		"no name":           "viper-attach 0 3\nabc",
	} {
		if _, _, err := natsmcp.ReadFrame(strings.NewReader(in), 100, 1000); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// viper nats send --pdf and the like: the command on the machine takes the frame, waits until the receiver has said that it
// understands attachments, puts the file into the store and sends the message.
func TestTheCommandSendsAFileOfItsFrameAndTheReceiverCanFetchIt(t *testing.T) {
	g := newObjectGroup(t, true)
	cfg := g.a.svc.Cfg()
	cfg.Quiet = true
	cfg.Names.Me = "a"
	data := png(2000)
	var out, errb bytes.Buffer
	// the connection of the command is another connection of the same user: the machine itself stays connected
	code := natsmcp.RunCLI(context.Background(), cfg, "send", []string{"--to", "b", "--attach"}, strings.NewReader(natsmcp.AttachFrame("see", "pic.png", data)), &out, &errb)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out.String(), errb.String())
	}
	var line natsmcp.Line
	if err := json.Unmarshal(out.Bytes(), &line); err != nil || line.Event != "sent" || line.AttType != "png" || line.AttName != "pic.png" || line.AttSize != int64(len(data)) || len(line.AttID) != 32 {
		t.Fatalf("%+v %v %s", line, err, out.String())
	}
	eventually(t, "b has the message", func() bool { return len(g.b.svc.Inbox().Since(0)) == 1 })
	if e := g.b.svc.Inbox().Since(0)[0]; e.Att == nil || e.Att.ID != line.AttID || e.Att.SHA256 != line.AttSHA || e.Text != "see" {
		t.Errorf("%+v", e)
	}
	// the operator-style fetch: the ID and the checksum from the message, into a folder
	dir := t.TempDir()
	out.Reset()
	code = natsmcp.RunCLI(context.Background(), cfg, "fetch", []string{"--id", line.AttID, "--sha256", line.AttSHA, "--out", dir}, strings.NewReader(""), &out, &errb)
	if code != 0 || !strings.Contains(out.String(), `"event":"fetched"`) {
		t.Fatalf("%d %s %s", code, out.String(), errb.String())
	}
	if got, err := os.ReadFile(filepath.Join(dir, line.AttID+".png")); err != nil || !bytes.Equal(got, data) {
		t.Errorf("%v", err)
	}
	wrong := strings.Repeat("0", 64)
	out.Reset()
	errb.Reset()
	if code = natsmcp.RunCLI(context.Background(), cfg, "fetch", []string{"--id", line.AttID, "--sha256", wrong, "--out", dir}, strings.NewReader(""), &out, &errb); code == 0 || !strings.Contains(errb.String(), "checksum") {
		t.Errorf("a wrong checksum: %d %s", code, errb.String())
	}
	// a folder the user names is not pruned: files that are there stay
	if err := os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code = natsmcp.RunCLI(context.Background(), cfg, "fetch", []string{"--id", line.AttID, "--sha256", line.AttSHA, "--out", dir}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "mine.txt")); err != nil {
		t.Error("a file of the user went")
	}
}

func TestTheCommandRefusesAFileForAMachineThatDoesNotUnderstandIt(t *testing.T) {
	g := newObjectGroup(t, false)
	cfg := g.a.svc.Cfg()
	cfg.Quiet = true
	var out, errb bytes.Buffer
	code := natsmcp.RunCLI(context.Background(), cfg, "send", []string{"--to", "b", "--attach"}, strings.NewReader(natsmcp.AttachFrame("see", "pic.png", png(100))), &out, &errb)
	if code == 0 || !strings.Contains(errb.String(), "b did not say that it understands attachments") {
		t.Errorf("%d %s", code, errb.String())
	}
	if n := len(g.b.svc.Inbox().Since(0)); n != 0 {
		t.Errorf("nothing was sent: %d", n)
	}
}
