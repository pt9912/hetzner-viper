package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// runDown is stop.sh: the checks, the confirmations and the deletion. Answers are read from in
// (a terminal or a pipe); prompts and reports go to stderr, like the script's.
func runDown(ctx context.Context, o *options, env settings.Env, in io.Reader, stdout, stderr io.Writer) error {
	c, err := o.host().Services.Settings.Load(settingsDir(o), o.machine, env, true)
	if err != nil {
		return err
	}
	l, err := o.host().Services.OpenLog(c, "down", stdout, stderr)
	if err != nil {
		return err
	}
	ctx, cancel := notifyContext(ctx)
	defer cancel()

	down := o.host().Services.Down(c, driving.DownOptions{Ask: newAsker(ctx, in, l.Err())})
	code, err := down.Down(ctx, c, o.force, l, l.Out(), l.Err())
	switch {
	case ctx.Err() != nil:
		fmt.Fprintln(l.Err())
		l.Interrupted()
		l.Close(ctx.Err(), 130)
		return exitStatus(130)
	case err != nil && !errors.Is(err, driving.ErrShown):
		l.Close(err, 1)
		return err
	case err != nil:
		l.Close(err, code)
		return exitStatus(code)
	}
	l.Close(nil, code)
	if code != 0 {
		return exitStatus(code)
	}
	return nil
}

// newAsker returns the function that shows a prompt and reads the answer, like `read -r -p`:
// one line, surrounding spaces and tabs ignored, a final line without a line end is no answer
// (the script aborted there), and an end of input or an interruption is an empty answer, which
// confirms nothing. One reader serves all prompts, so answers that follow in a pipe are not lost.
func newAsker(ctx context.Context, in io.Reader, prompts io.Writer) func(string) string {
	lines := make(chan string)
	go func() {
		r := bufio.NewReader(in)
		for {
			line, err := r.ReadString('\n')
			if err != nil { // end of input, also after text without a line end
				close(lines)
				return
			}
			select {
			case lines <- strings.Trim(strings.TrimSuffix(line, "\n"), " \t"):
			case <-ctx.Done():
				return
			}
		}
	}()
	return func(prompt string) string {
		fmt.Fprint(prompts, prompt)
		if ctx.Err() != nil {
			return ""
		}
		select {
		case line, ok := <-lines:
			if !ok || ctx.Err() != nil {
				return ""
			}
			return line
		case <-ctx.Done():
			return ""
		}
	}
}

func nativeDown(o *options) error {
	if err := o.host().need("ssh"); err != nil {
		return err
	}
	env, err := o.host().Env()
	if err != nil {
		return err
	}
	return runDown(context.Background(), o, env, o.host().Stdin, o.host().Stdout, o.host().Stderr)
}
