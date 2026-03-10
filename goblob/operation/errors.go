package operation

import (
	"errors"
	"fmt"
)

var ErrNoWritableVolumes = errors.New("no writable volumes available")
var ErrVolumeNotFound = errors.New("volume not found")

// HTTPStatusError is returned when a non-2xx response is received.
type HTTPStatusError struct {
	Op         string
	URL        string
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("%s request to %s failed: status=%d body=%q", e.Op, e.URL, e.StatusCode, e.Body)
}
