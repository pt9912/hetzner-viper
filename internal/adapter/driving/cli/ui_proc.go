package cli

import (
	"context"
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
		cmd := exec.CommandContext(context.Background(), exe, append(argv, "--dir", dir)...) //nolint:gosec // the arguments are made of names from the settings files and fixed words
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
			p.done <- exitCode(err)
		}()
		return p, nil
	}
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
