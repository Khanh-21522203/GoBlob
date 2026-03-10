package storeloader

import (
	"fmt"
	"strings"

	"GoBlob/goblob/filer"
	"GoBlob/goblob/filer/cassandra"
	"GoBlob/goblob/filer/leveldb2"
	"GoBlob/goblob/filer/mysql2"
	"GoBlob/goblob/filer/postgres2"
	"GoBlob/goblob/filer/redis3"
)

// LoadFilerStoreFromConfig selects backend by config["backend"].
func LoadFilerStoreFromConfig(config map[string]string) (filer.FilerStore, error) {
	backend := strings.ToLower(strings.TrimSpace(config["backend"]))
	if backend == "" {
		backend = "leveldb2"
	}

	switch backend {
	case "leveldb2":
		dir := strings.TrimSpace(config["leveldb2.dir"])
		if dir == "" {
			dir = strings.TrimSpace(config["store.dir"])
		}
		if dir == "" {
			dir = "./tmp/filer"
		}
		return leveldb2.NewLevelDB2Store(dir)
	case "redis3":
		return redis3.NewRedis3Store(config)
	case "postgres2":
		return postgres2.NewPostgres2Store(config)
	case "mysql2":
		return mysql2.NewMySQL2Store(config)
	case "cassandra":
		return cassandra.NewCassandraStore(config)
	default:
		return nil, fmt.Errorf("unsupported filer backend: %s", backend)
	}
}
