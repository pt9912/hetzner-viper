package start

import _ "embed"

// Scripts that run on the server. Each is fed to `bash -s -- <args>` on stdin.
var (
	//go:embed scripts/swap.sh
	swapScript string
	//go:embed scripts/gitid.sh
	gitIDScript string
	//go:embed scripts/clone.sh
	cloneScript string
	//go:embed scripts/postclone.sh
	postCloneScript string
	//go:embed scripts/brewpkg.sh
	brewPkgScript string
	//go:embed scripts/cdline.sh
	cdLineScript string
	//go:embed scripts/inventory.sh
	inventoryScript string
)
