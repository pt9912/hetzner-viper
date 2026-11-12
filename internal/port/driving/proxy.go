package driving

import (
	"context"
	"io"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// Proxy connects standard input and output to the ssh port of the machine through the API of its provider, until
// either side closes (viper k8s-proxy, the ProxyCommand of the ssh config of a machine that is a pod).
type Proxy func(ctx context.Context, c *model.Machine, in io.Reader, out io.Writer) error
