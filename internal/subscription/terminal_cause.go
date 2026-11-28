package subscription

import (
	"errors"
	"fmt"
)

// The observation seam retains only a cause. Ownership-bearing audit wrappers
// continue to belong exclusively to Next and must not survive through this field.
func stableTerminalCause(err error) error {
	out, _ := stripTerminalOwnership(err)
	return out
}

func stripTerminalOwnership(err error) (error, bool) {
	switch e := err.(type) { //nolint:errorlint // Rewrite each node recursively; errors.As would discard joined sibling causes.
	case *TerminalError:
		return stableTerminalCause(e.Cause), true
	case *TransferError:
		return stableTerminalCause(e.Cause), true
	case *contentTerminalError:
		return stableTerminalCause(e.Cause), true
	case *LogsTerminalError:
		return stableTerminalCause(e.Cause), true
	case *ReceiptsTerminalError:
		return stableTerminalCause(e.Cause), true
	case interface{ Unwrap() []error }:
		original := e.Unwrap()
		cleaned := make([]error, len(original))
		changed := false
		for i, child := range original {
			var c bool
			cleaned[i], c = stripTerminalOwnership(child)
			changed = changed || c
		}
		if changed {
			return errors.Join(cleaned...), true
		}
	case interface{ Unwrap() error }:
		child, changed := stripTerminalOwnership(e.Unwrap())
		if changed {
			return fmt.Errorf("%s: %w", err.Error(), child), true
		}
	}
	return err, false
}
