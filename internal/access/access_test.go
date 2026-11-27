package access

import (
	"fmt"
	"testing"
)

func TestDenied(t *testing.T) {
	if !IsDenied(ErrManage) || !IsDenied(fmt.Errorf("wrapped: %w", ErrManage)) || IsDenied(fmt.Errorf("other")) {
		t.Error("IsDenied")
	}
}
