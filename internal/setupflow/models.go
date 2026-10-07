package setupflow

import (
	"github.com/glebenator/cvkeharness/internal/modelcatalog"
	"time"
)

// Retains the established stale-cache test boundary. Discovery lives in modelcatalog.
func fetchCodexModels(now time.Time) ModelResult {
	return modelcatalog.FetchCodexModels(now, "")
}
