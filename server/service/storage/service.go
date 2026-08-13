package storage

import "os"

type Service struct {
	remote *remoteMediaManager
}

func NewService() *Service {
	_ = os.Remove(sentinelPath)
	recoverUSBAtStartup()
	return &Service{remote: newRemoteMediaManager()}
}
