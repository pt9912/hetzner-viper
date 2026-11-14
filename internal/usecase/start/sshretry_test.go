package start_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/usecase/start"
)

// A fresh server closes a connection now and then before the login (kex_exchange_identification): nothing of the command has run,
// so the step tries again; any other failure is the step's.
func TestACommandIsTriedAgainWhenTheServerClosesTheConnectionBeforeTheLogin(t *testing.T) {
	const kex = "kex_exchange_identification: read: Connection reset by peer\nConnection reset by 203.0.113.7 port 22\n"
	run := func(stderrs []string, errs []error) (calls int, out, errOut string, slept int, err error) {
		var o, e bytes.Buffer
		d := start.Deps{
			Stream: func(_ context.Context, _ string, stdout, stderr io.Writer, _ string, _ ...string) error {
				i := calls
				calls++
				_, _ = io.WriteString(stderr, stderrs[i])
				_, _ = io.WriteString(stdout, "out\n")
				return errs[i]
			},
			Sleep: func(time.Duration) { slept++ },
		}
		err = start.RunSSH(d, &model.Machine{SSHConfig: "cfg", SSHAlias: "alias"}, &o, &e, "true")
		return calls, o.String(), e.String(), slept, err
	}
	fail := errors.New("exit status 255")
	// closed twice, then it works: three calls, the closed attempts are not shown
	calls, _, errOut, slept, err := run([]string{kex, kex, "warning: x\n"}, []error{fail, fail, nil})
	if err != nil || calls != 3 || slept != 2 || errOut != "warning: x\n" {
		t.Errorf("calls %d slept %d err %v stderr %q", calls, slept, err, errOut)
	}
	// closed every time: five tries, then the error and what ssh said once
	all := []string{kex, kex, kex, kex, kex}
	errs := []error{fail, fail, fail, fail, fail}
	calls, _, errOut, _, err = run(all, errs)
	if err == nil || calls != 5 || strings.Count(errOut, "kex_exchange_identification") != 1 {
		t.Errorf("calls %d err %v stderr %q", calls, err, errOut)
	}
	// another failure is not tried again, and its output is passed on
	calls, _, errOut, _, err = run([]string{"Permission denied (publickey).\n"}, []error{fail})
	if err == nil || calls != 1 || errOut != "Permission denied (publickey).\n" {
		t.Errorf("calls %d err %v stderr %q", calls, err, errOut)
	}
	// a command that works is run once, its progress without a newline goes on at once
	calls, out, errOut, _, err := run([]string{"progress 50%"}, []error{nil})
	if err != nil || calls != 1 || out != "out\n" || errOut != "progress 50%" {
		t.Errorf("calls %d err %v out %q stderr %q", calls, err, out, errOut)
	}
}
