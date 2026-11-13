package model

import "fmt"

// ElsewhereError is a machine that was started in another group than the one asked for: it is managed where it was started.
type ElsewhereError struct{ Machine, Running, Asked string }

func (e *ElsewhereError) Error() string {
	return fmt.Sprintf("The machine %s runs in the group %s (it was started there) and is managed there (--in-group %s). To start it in the group %s: viper down %s --in-group %s first", e.Machine, e.Running, e.Running, e.Asked, e.Machine, e.Running)
}
