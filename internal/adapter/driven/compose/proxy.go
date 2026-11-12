package compose

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// proxyScript runs inside the container: it connects its standard input and output to sshd on the loopback address
// (bash has /dev/tcp, the image has no nc). The second cat reads descriptor 0 explicitly: a background job of a shell
// that is not interactive would get /dev/null. wait -n ends the script when one side is done, and the kill takes the
// other cat with it: docker exec does not end processes it started when the client dies, so without it they would pile
// up (docs/plan/compose.md §15).
func proxyScript() string {
	return fmt.Sprintf(`exec 3<>/dev/tcp/127.0.0.1/%d; cat <&3 & p=$!; cat <&0 >&3 & q=$!; wait -n; kill $p $q 2>/dev/null; true`, SSHPort)
}

// Proxy connects in and out to the ssh port of the container through docker exec, until either side closes. Nothing but
// the ssh data may go to out.
func (c *Client) Proxy(ctx context.Context, name string, in io.Reader, out io.Writer) error {
	if err := checkName(name); err != nil {
		return err
	}
	if err := c.checkTarget(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "docker", "--context", c.opts.Context, "exec", "-i", name, "bash", "-c", proxyScript()) //nolint:gosec // the context and the name are checked above
	var stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, &stderr
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil //nolint:nilerr // the user ended it
		}
		return fmt.Errorf("docker exec into %s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
