package gocaskDB

import "os"

type hashBody struct {
	file os.File
	vsz int32
	vpos uint32
	timestamp int64
}

func RebuildHashFromHint(db *DB) map[Key]hashBody {
	return nil
}