// Package deploy bundles installation assets into the same sbmgr executable.
package deploy

import "embed"

//go:embed install-systemd.sh path-lib.sh deploy-release.sh setup-ip-https.sh renew-ip-https.sh mesh-agent-rpc.sh fleet-readonly-snapshot.sh *.in sbmgr-ip-cert-renew.timer certbot-requirements.txt
var Assets embed.FS
