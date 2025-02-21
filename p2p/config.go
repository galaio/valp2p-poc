package p2p

import (
	"time"

	"github.com/ethereum/go-ethereum/log"
)

const (
	defaultPubsubQueueSize = 600
)

type Config struct {
	StaticPeers    []string // using multi address format
	BootStrapAddrs []string
	HostAddress    string // it indicates listen IP addr, it better a external IP for public service
	DataDir        string // the dir saves the key and cached nodes
	QUICPort       uint
	TCPPort        uint
	PingInterval   time.Duration
	MaxPeers       uint
	QueueSize      uint
	EnableQuic     bool
}

func (cfg *Config) SanityCheck() error {
	if cfg.QueueSize == 0 {
		log.Warn("Invalid pubsub queue size of %d, set %d", cfg.QueueSize, defaultPubsubQueueSize)
		cfg.QueueSize = defaultPubsubQueueSize
	}
	return nil
}
