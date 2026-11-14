package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
)

// process is viper started as a program of its own: the actions of the pages run as at the terminal, with the same
// locks, logs and questions (docs/plan/ui.md §4).
type process struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *io.PipeReader
	done chan int
}

// spawner starts this program with the arguments and the settings directory of viper ui. Standard input stays open: a
// question is answered with a line.
func spawner(dir string) web.Spawner {
	return func(argv []string) (web.Proc, error) {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		p, err := startProcess(exe, append(argv, "--dir", dir))
		if err != nil {
			return nil, err // not a nil *process inside an interface
		}
		return p, nil
	}
}

// startProcess runs the program with the standard input open and standard output and error in one pipe.
func startProcess(exe string, args []string) (*process, error) {
	cmd := exec.CommandContext(context.Background(), exe, args...) //nolint:gosec // the arguments are made of names from the settings files and fixed words
	prepare(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &process{cmd: cmd, in: in, out: pr, done: make(chan int, 1)}
	go func() {
		err := cmd.Wait()
		_ = pw.Close()
		p.done <- statusOf(cmd, err)
	}()
	return p, nil
}

// statusOf is the exit status of the process: 0 for none, and for a program that keeps the pipe open after it ended (it went
// to the background) the status of the process, not the error of Wait.
func statusOf(cmd *exec.Cmd, err error) int {
	if err == nil {
		return 0 // exitCode(nil) is 1: it is meant for an error
	}
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	return exitCode(err)
}

func (p *process) Output() io.Reader { return p.out }
func (p *process) Input() io.Writer  { return p.in }
func (p *process) Interrupt() error  { return interrupt(p.cmd) }
func (p *process) Kill() error       { return p.cmd.Process.Kill() }

func (p *process) Wait() int {
	code := <-p.done
	_ = p.in.Close()
	return code
}
