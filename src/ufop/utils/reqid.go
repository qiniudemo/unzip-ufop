package utils

import (
	"encoding/base64"
	"encoding/binary"
	"os"
	"time"
)

var pid = uint32(os.Getpid())

func NewRequestId() string {
	var b [12]byte
	binary.LittleEndian.PutUint32(b[:], pid)
	binary.LittleEndian.PutUint64(b[4:], uint64(time.Now().UnixNano()))
	return base64.URLEncoding.EncodeToString(b[:])
}

func DecodeRequestId(reqId string) (uint, int64) {
	b, err := base64.URLEncoding.DecodeString(reqId)
	if err != nil || len(b) < 12 {
		return 0, 0
	}
	pid := binary.LittleEndian.Uint32(b[:4])
	unixNano := binary.LittleEndian.Uint64(b[4:])
	return uint(pid), int64(unixNano)
}
