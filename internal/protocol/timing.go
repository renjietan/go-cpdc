package protocol

import "time"

// All CPDC protocol windows live here so deployments can tune one file.
const (
	DiscoverInterval       = time.Second
	DiscoverWindow         = 5 * time.Second
	AuthInterval           = time.Second
	AuthWindow             = 5 * time.Second
	RecentDiscoverValidity = 10 * time.Second
	TransferSilenceTimeout = 10 * time.Second
	ClientSessionIdle      = 30 * time.Second
	ParseTimeout           = 30 * time.Second
	ParseResultRetry       = time.Second
	ParseResultMaxAttempts = 5
	EventLoopPoll          = 100 * time.Millisecond
)
