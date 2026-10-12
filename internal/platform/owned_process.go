package platform

import "errors"

// ErrWorkSuperseded is a quiet result for an obsolete internal picker request.
var ErrWorkSuperseded = errors.New("interactive work superseded")
