package command

import (
	"fmt"
	"strings"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/quota"
	"GoBlob/goblob/s3api"
)

func newQuotaManager(filerAddr string) (*quota.Manager, error) {
	addr := strings.TrimSpace(filerAddr)
	if addr == "" {
		return nil, fmt.Errorf("filer address is required")
	}
	fc := s3api.NewFilerClient([]types.ServerAddress{types.ServerAddress(addr)}, "/buckets")
	return quota.NewManager(fc), nil
}
