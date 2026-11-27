package transcript

import (
	"testing"

	"github.com/kumoSleeping/iridium/internal/api"
)

func TestSelfStopped(t *testing.T) {
	stop := `{"command":"ir api POST internal/sessions/0b5e2c1a-7d3f-4e8a-9c21-5f6a7b8c9d0e/interrupt","description":"End turn"}`
	said := "The user doesn't want to proceed with this tool use. The tool use was rejected. Correction to the text above: this call was stopped while running, not refused before it ran."
	paired := paired([]api.Block{
		{At: "1.0", Kind: api.BlockKindTool, ID: "a", Name: "Bash", Input: stop},
		{At: "2.0", Kind: api.BlockKindResult, ID: "a", Text: said, Error: true},
		{At: "3.0", Kind: api.BlockKindTool, ID: "b", Name: "Bash", Input: stop},
		{At: "4.0", Kind: api.BlockKindResult, ID: "b", Text: "Error: Exit code 1\nir: 403 forbidden", Error: true},
		{At: "5.0", Kind: api.BlockKindTool, ID: "c", Name: "Bash", Input: `{"command":"sleep 100"}`},
		{At: "6.0", Kind: api.BlockKindResult, ID: "c", Text: said, Error: true},
	})
	if len(paired) != 3 {
		t.Fatalf("paired: %d blocks", len(paired))
	}
	for i, want := range []bool{false, true, true} {
		if got := paired[i].Result.Error; got != want {
			t.Errorf("%s: error %v, want %v", paired[i].ID, got, want)
		}
	}
}
