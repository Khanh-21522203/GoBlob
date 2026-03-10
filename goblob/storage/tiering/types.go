package tiering

import "time"

const (
	DiskTypeSSD     = "ssd"
	DiskTypeHDD     = "hdd"
	DiskTypeDefault = ""
)

// Tier describes migration thresholds for one tiering policy.
type Tier struct {
	DiskType    string
	AccessAge   time.Duration
	ArchiveAge  time.Duration
	RemoteStore string
}
