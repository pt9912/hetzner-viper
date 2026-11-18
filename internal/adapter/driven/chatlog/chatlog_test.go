package chatlog_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/chatlog"
	port "github.com/pt9912/hetzner-viper/internal/port/driven/chatlog"
)

func rec(from, text string) port.Record {
	return port.Record{Dir: "in", From: from, To: "operator", Type: "msg", Text: text}
}

func TestEveryRecordGetsTheNextNumberOfTheGroupAndGoesIntoEachConversation(t *testing.T) {
	s := chatlog.Memory(0)
	a, _ := s.Append("dev", []string{"rechner__w1", "alle"}, rec("w1", "one"))
	b, _ := s.Append("dev", []string{"thema__build", "alle"}, rec("w2", "two"))
	if a.N != 1 || b.N != 2 || a.T.IsZero() || a.T.Location() != time.UTC {
		t.Fatalf("%+v %+v", a, b)
	}
	all, _ := s.Tail("dev", "alle", 10)
	w1, _ := s.Tail("dev", "rechner__w1", 10)
	if len(all) != 2 || len(w1) != 1 || w1[0].N != 1 || all[1].Text != "two" {
		t.Errorf("all %v, w1 %v", all, w1)
	}
	after, _ := s.After("dev", "alle", 1, 10)
	if len(after) != 1 || after[0].N != 2 {
		t.Errorf("after 1: %v", after)
	}
	if other, _ := s.Append("test", []string{"alle"}, rec("w1", "x")); other.N != 1 {
		t.Errorf("each group counts for itself: %d", other.N)
	}
}

func TestTheListHasTheNewestFirstAndTheUnreadCountsOfWhatIsForTheOperator(t *testing.T) {
	s := chatlog.Memory(0)
	_, _ = s.Append("dev", []string{"rechner__w1"}, rec("w1", "a"))
	_, _ = s.Append("dev", []string{"rechner__w2"}, rec("w2", "b"))
	_, _ = s.Append("dev", []string{"rechner__w2"}, rec("w2", "c"))
	mine := rec("operator", "answer")
	mine.Dir = "out"
	_, _ = s.Append("dev", []string{"rechner__w2"}, mine)
	cs, _ := s.Conversations("dev")
	if len(cs) != 2 || cs[0].Key != "rechner__w2" || cs[0].Unread != 2 || cs[1].Unread != 1 || cs[0].Last.Dir != "out" {
		t.Fatalf("%+v", cs)
	}
	_ = s.MarkRead("dev", "rechner__w2", 3)
	cs, _ = s.Conversations("dev")
	if cs[0].Unread != 0 {
		t.Errorf("read up to 3: %+v", cs[0])
	}
	if n, _ := s.Read("dev", "rechner__w2"); n != 3 {
		t.Errorf("the marker is %d", n)
	}
}

func TestTheNumbersGoOnAfterARestartAndTheMarkersStay(t *testing.T) {
	dir := t.TempDir()
	s, err := chatlog.Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		_, _ = s.Append("dev", []string{"alle", "rechner__w1"}, rec("w1", "m"))
	}
	_ = s.MarkRead("dev", "rechner__w1", 4)
	// not closed: a killed program writes the number only now and then; the files have the truth
	s2, _ := chatlog.Open(dir, 0)
	r, _ := s2.Append("dev", []string{"alle"}, rec("w1", "after the restart"))
	if r.N != 8 {
		t.Errorf("the number after a restart is %d, not 8", r.N)
	}
	if n, _ := s2.Read("dev", "rechner__w1"); n != 4 {
		t.Errorf("the marker is %d", n)
	}
	cs, _ := s2.Conversations("dev")
	if len(cs) != 2 {
		t.Errorf("the list after a restart: %+v", cs)
	}
}

func TestFilesAndFoldersAreOnlyForTheUser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes")
	}
	dir := filepath.Join(t.TempDir(), "chat")
	s, _ := chatlog.Open(dir, 0)
	_, _ = s.Append("dev", []string{"alle"}, rec("w1", "x"))
	_ = s.Close()
	for _, p := range []string{dir, filepath.Join(dir, "dev")} {
		if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", p, st, err)
		}
	}
	for _, p := range []string{filepath.Join(dir, "dev", "alle.jsonl"), filepath.Join(dir, "dev", ".n")} {
		if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", p, st, err)
		}
	}
}

func TestAConversationOrAGroupWithAPathInItsNameIsRefused(t *testing.T) {
	s := chatlog.Memory(0)
	for _, key := range []string{"../x", "rechner__../../etc", "paar__a", "RECHNER__w1", "rechner__w_1", "thema__", "alle/../x", "rechner__w1.jsonl"} {
		if _, err := s.Append("dev", []string{key}, rec("w1", "x")); err == nil {
			t.Errorf("%q was taken", key)
		}
		if _, err := s.Tail("dev", key, 1); err == nil {
			t.Errorf("%q was read", key)
		}
	}
	for _, group := range []string{"../dev", "DEV", "a/b", "", "d_v"} {
		if _, err := s.Append(group, []string{"alle"}, rec("w1", "x")); err == nil {
			t.Errorf("the group %q was taken", group)
		}
	}
}

func TestAFullFileBecomesTheOlderOneAndTheOldestLinesFallAway(t *testing.T) {
	dir := t.TempDir()
	s, _ := chatlog.Open(dir, 0)
	for i := 0; i < chatlog.RotateLines*2+10; i++ { // two blocks full and a few lines: the first block is gone
		_, _ = s.Append("dev", []string{"alle"}, rec("w1", "x"))
	}
	all, _ := s.Tail("dev", "alle", 100000)
	if len(all) < chatlog.RotateLines || len(all) > chatlog.RotateLines*2 || all[len(all)-1].N != int64(chatlog.RotateLines*2+10) {
		t.Errorf("%d records, the last is %d", len(all), all[len(all)-1].N)
	}
	for i := 1; i < len(all); i++ {
		if all[i].N != all[i-1].N+1 {
			t.Fatalf("the numbers are not in a row at %d", i)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "dev", "alle.1.jsonl")); err != nil {
		t.Errorf("the older file: %v", err)
	}
}

func TestRecordsOlderThanTheKeepTimeAreNotShownAndPruneRemovesOldFiles(t *testing.T) {
	dir := t.TempDir()
	s, _ := chatlog.Open(dir, 30)
	old := rec("w1", "old")
	old.T = time.Now().Add(-40 * 24 * time.Hour)
	_, _ = s.Append("dev", []string{"rechner__w1"}, old)
	_, _ = s.Append("dev", []string{"rechner__w1"}, rec("w1", "new"))
	got, _ := s.Tail("dev", "rechner__w1", 10)
	if len(got) != 1 || got[0].Text != "new" {
		t.Errorf("%+v", got)
	}
	_, _ = s.Append("dev", []string{"thema__stale"}, rec("w1", "x"))
	f := filepath.Join(dir, "dev", "thema__stale.jsonl")
	past := time.Now().Add(-45 * 24 * time.Hour)
	if err := os.Chtimes(f, past, past); err != nil {
		t.Fatal(err)
	}
	_ = s.Prune("dev", time.Now())
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Errorf("an old file stays: %v", err)
	}
	if cs, _ := s.Conversations("dev"); len(cs) != 1 {
		t.Errorf("the list follows the files: %+v", cs)
	}
}

func TestDeleteAndSearch(t *testing.T) {
	s := chatlog.Memory(0)
	_, _ = s.Append("dev", []string{"rechner__w1", "alle"}, rec("w1", "The Cache is EMPTY"))
	_, _ = s.Append("dev", []string{"rechner__w2", "alle"}, rec("w2", "nothing"))
	hits, err := s.Search("dev", "cache", 10)
	if err != nil || len(hits) != 2 { // the record is in two conversations
		t.Errorf("%v %v", hits, err)
	}
	if hits, _ := s.Search("dev", "cache", 1); len(hits) != 1 {
		t.Errorf("the limit: %v", hits)
	}
	_ = s.Delete("dev", "rechner__w1")
	if got, _ := s.Tail("dev", "rechner__w1", 10); len(got) != 0 {
		t.Errorf("deleted: %v", got)
	}
	if got, _ := s.Tail("dev", "alle", 10); len(got) != 2 {
		t.Errorf("the others stay: %v", got)
	}
	_ = s.Delete("dev", "")
	if cs, _ := s.Conversations("dev"); len(cs) != 0 {
		t.Errorf("all deleted: %+v", cs)
	}
}

func TestALongTextIsCutAndANonRecordLineIsSkipped(t *testing.T) {
	dir := t.TempDir()
	s, _ := chatlog.Open(dir, 0)
	r, _ := s.Append("dev", []string{"alle"}, rec("w1", strings.Repeat("x", 100000)))
	if len(r.Text) > 32<<10 {
		t.Errorf("the text has %d bytes", len(r.Text))
	}
	f := filepath.Join(dir, "dev", "alle.jsonl")
	b, _ := os.ReadFile(f)
	_ = os.WriteFile(f, append([]byte("not json\n"), b...), 0o600)
	s2, _ := chatlog.Open(dir, 0)
	if got, _ := s2.Tail("dev", "alle", 10); len(got) != 1 {
		t.Errorf("%d records", len(got))
	}
}
