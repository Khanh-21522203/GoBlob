package security

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

const MaxFilenameLength = 255

var (
	ErrInvalidVolumeId       = errors.New("invalid volume id")
	ErrVolumeIdOutOfRange    = errors.New("volume id out of range")
	ErrPathTraversalDetected = errors.New("path traversal detected")
	ErrFilenameTooLong       = errors.New("filename too long")
	ErrInvalidFilename       = errors.New("invalid filename")
)

func ValidateVolumeId(vid string) error {
	u, err := strconv.ParseUint(strings.TrimSpace(vid), 10, 32)
	if err != nil {
		return ErrInvalidVolumeId
	}
	if u > uint64(^uint32(0)) {
		return ErrVolumeIdOutOfRange
	}
	return nil
}

func ValidatePath(p string) error {
	decoded, _ := url.PathUnescape(strings.TrimSpace(p))
	if strings.Contains(decoded, "..") {
		return ErrPathTraversalDetected
	}
	return nil
}

func ValidateFilename(name string) error {
	n := strings.TrimSpace(name)
	if len(n) == 0 || n == "." || n == ".." {
		return ErrInvalidFilename
	}
	if len(n) > MaxFilenameLength {
		return ErrFilenameTooLong
	}
	return nil
}
