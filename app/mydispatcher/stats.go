package mydispatcher

import "github.com/xtls/xray-core/app/dispatcher"

// Use the core writer so Vision can account for bytes transferred by splice.
type SizeStatWriter = dispatcher.SizeStatWriter
