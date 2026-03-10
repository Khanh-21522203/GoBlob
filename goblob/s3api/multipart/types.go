package multipart

import "time"

// UploadedPart stores one uploaded multipart segment.
type UploadedPart struct {
	PartNumber int
	ETag       string
	Size       int64
	Data       []byte
}

// MultipartUpload stores one in-progress multipart upload.
type MultipartUpload struct {
	UploadID  string
	Bucket    string
	Key       string
	CreatedAt time.Time
	Parts     map[int]*UploadedPart
}

// CompleteMultipartUploadRequest is the XML payload for completion.
type CompleteMultipartUploadRequest struct {
	Parts []CompletePart `xml:"Part"`
}

// CompletePart identifies a completed part in order.
type CompletePart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}
