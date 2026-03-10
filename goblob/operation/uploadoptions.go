package operation

// UploadOption controls assign/upload behavior.
type UploadOption struct {
	Master      string
	Collection  string
	Replication string
	Ttl         string
	DataCenter  string
	Rack        string
	DiskType    string
	Count       uint64
	Preallocate int64
}

// AssignOption is kept as an alias for backward compatibility.
type AssignOption = UploadOption

// UploadOptionFunc applies a single option mutation.
type UploadOptionFunc func(*UploadOption)

// NewUploadOption creates an UploadOption with defaults and functional overrides.
func NewUploadOption(master string, fns ...UploadOptionFunc) *UploadOption {
	opt := &UploadOption{
		Master: master,
		Count:  1,
	}
	for _, fn := range fns {
		if fn != nil {
			fn(opt)
		}
	}
	return opt
}

func WithCollection(collection string) UploadOptionFunc {
	return func(opt *UploadOption) { opt.Collection = collection }
}

func WithReplication(replication string) UploadOptionFunc {
	return func(opt *UploadOption) { opt.Replication = replication }
}

func WithTTL(ttl string) UploadOptionFunc {
	return func(opt *UploadOption) { opt.Ttl = ttl }
}

func WithDataCenter(dc string) UploadOptionFunc {
	return func(opt *UploadOption) { opt.DataCenter = dc }
}

func WithRack(rack string) UploadOptionFunc {
	return func(opt *UploadOption) { opt.Rack = rack }
}

func WithDiskType(diskType string) UploadOptionFunc {
	return func(opt *UploadOption) { opt.DiskType = diskType }
}

func WithCount(count uint64) UploadOptionFunc {
	return func(opt *UploadOption) {
		if count > 0 {
			opt.Count = count
		}
	}
}

func WithPreallocate(preallocate int64) UploadOptionFunc {
	return func(opt *UploadOption) { opt.Preallocate = preallocate }
}
