package storage

import "os"

type Service struct {
	remote *remoteMediaManager
}

func NewService() *Service {
	_ = os.Remove(sentinelPath)
	return &Service{remote: newRemoteMediaManager()}
}
