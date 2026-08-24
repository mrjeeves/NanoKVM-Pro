package storage

import "os"

type Service struct {
	remote *remoteMediaManager
}

func NewService() *Service {
	_ = os.Remove(sentinelPath)
	recoverUSBAtStartup()
	// recoverUSBAtStartup repairs what an interrupted process left behind, once.
	// The watchdog covers everything after that — a link that dies while the
	// server is up, which nothing used to notice.
	StartUSBWatchdog()
	return &Service{remote: newRemoteMediaManager()}
}
