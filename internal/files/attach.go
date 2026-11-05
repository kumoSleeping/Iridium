package files

import (
	"fmt"
	"strings"

	"github.com/kumoSleeping/iridium/internal/agent"
)

const (
	MaxAttachments = 20
	// Images past this many bytes in one message go by path only (a request has a size cap).
	maxImageBytes = 20 << 20
)

// Message is text with the attachments (refs of session) added: each one's path on a line of
// its own, in inline code so the transcript names it and it gets a ref again, and the images
// Claude can see, within its limits.
func (s *Service) Message(session, text string, refs []string) (string, []agent.Image, error) {
	if len(refs) == 0 {
		return text, nil, nil
	}
	if len(refs) > MaxAttachments {
		return "", nil, fmt.Errorf("%w: at most %d attachments", ErrBad, MaxAttachments)
	}
	var lines []string
	var images []agent.Image
	total := 0
	for _, r := range refs {
		p, err := s.Lookup(session, r)
		if err != nil {
			return "", nil, fmt.Errorf("attachment %s: %w", r, err)
		}
		if strings.ContainsAny(p, "`\n") {
			return "", nil, fmt.Errorf("%w: attachment %s has a backquote or newline in its path", ErrBad, r)
		}
		lines = append(lines, "[附件] `"+p+"`")
		info, err := s.Info(p)
		if err != nil {
			return "", nil, fmt.Errorf("attachment %s: %w", r, err)
		}
		if info.Kind != "image" {
			continue
		}
		if img, ok := ForClaude(p); ok && total+len(img.Data) <= maxImageBytes {
			images = append(images, img)
			total += len(img.Data)
		}
	}
	att := strings.Join(lines, "\n")
	if strings.TrimSpace(text) == "" {
		return att, images, nil
	}
	return text + "\n\n" + att, images, nil
}
