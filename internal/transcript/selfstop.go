package transcript

import (
	"regexp"
	"strings"

	"github.com/kumoSleeping/iridium/internal/api"
)

// An agent stopping its own turn (ir api POST internal/sessions/<its id>/interrupt, as the
// channel's rules tell it to when it has nothing to say; self in older records) is stopped in
// that very call: Claude records the
// call as stopped, an error. It did what it was for: not a failure (kumo, 2026-11-26: a red ⊗ on
// each). Only when its result says it was stopped; the call answered with an error stays one.
// web/src/chat/fold.ts selfStopped is the same rule for a result paired there.
var (
	selfStopCall = regexp.MustCompile(`\bir\s+api\s+(?:POST\s+)?/?(?:v1/)?internal/sessions/[\w.-]+/interrupt\b`)
	stoppedWords = []string{"stopped while running", "The user doesn't want to proceed with this tool use", "[Request interrupted by user"}
)

// SelfStopped reports whether tool call b, with its result, is its agent stopping its own turn.
func SelfStopped(b api.Block) bool {
	if b.Kind != api.BlockKindTool || b.Result == nil || !b.Result.Error || !selfStopCall.MatchString(b.Input) {
		return false
	}
	for _, w := range stoppedWords {
		if strings.Contains(b.Result.Text, w) {
			return true
		}
	}
	return false
}
