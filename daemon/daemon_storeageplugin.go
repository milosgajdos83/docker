// +build !exclude_graphdriver_plugin

package daemon

import (
	_ "github.com/docker/docker/daemon/graphdriver/storageplugin"
)
