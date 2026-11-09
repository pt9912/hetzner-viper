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
	//go:embed scripts/natsinstall.sh
	natsInstallScript string
	//go:embed scripts/natscert.sh
	natsCertScript string
	//go:embed scripts/natsidentity.sh
	natsIdentityScript string
	//go:embed scripts/natsconfig.sh
	natsConfigScript string
	//go:embed scripts/natsunit.sh
	natsUnitScript string
	//go:embed scripts/natscheck.sh
	natsCheckScript string
	//go:embed scripts/natscli.sh
	natsCLIScript string
	//go:embed scripts/natsread.sh
	natsReadScript string
	//go:embed scripts/natsfile.sh
	natsFileScript string
	//go:embed scripts/natsconnect.sh
	natsConnectScript string
)
