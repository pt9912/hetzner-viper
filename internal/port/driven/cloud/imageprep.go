package cloud

import (
	"context"
	"io"
)

// ImagePreparer is what a provider adds that has to bring the image of the machine to where the machines run before the
// first one is made: a k3d cluster that pulls it from a registry viper makes. It is a capability apart from Provider (ask
// with As); viper up calls it before it creates a server and says what it did on out.
type ImagePreparer interface {
	// PrepareImage makes the image available to the machines of this provider; the provider then uses the name it
	// returns for the machines it creates.
	PrepareImage(ctx context.Context, out io.Writer) error
}
