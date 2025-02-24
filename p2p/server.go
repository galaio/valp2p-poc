package p2p

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"net"
	"sync"
	"time"

	kaddht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/routing"

	"github.com/ethereum/go-ethereum/log"
	"github.com/libp2p/go-libp2p"
	mplex "github.com/libp2p/go-libp2p-mplex"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	libp2pquic "github.com/libp2p/go-libp2p/p2p/transport/quic"
	libp2ptcp "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	gomplex "github.com/libp2p/go-mplex"
	"github.com/multiformats/go-multiaddr"
	"github.com/pkg/errors"
	leakybucket "github.com/prysmaticlabs/prysm/v5/container/leaky-bucket"
)

type Server struct {
	started          bool
	ctx              context.Context
	cancel           context.CancelFunc
	cfg              *Config
	host             host.Host
	dht              *kaddht.IpfsDHT
	watcher          *PeerWatcher
	ipLimiter        *leakybucket.Collector
	privKey          *ecdsa.PrivateKey
	pubsub           *pubsub.PubSub
	joinedTopics     map[string]*pubsub.Topic
	joinedTopicsLock sync.RWMutex

	stop chan struct{}
}

func NewServer(cfg *Config) (*Server, error) {
	ctx, cancel := context.WithCancel(context.Background())
	_ = cancel // govet fix for lost cancel. Cancel is handled in service.Stop().

	if err := cfg.SanityCheck(); err != nil {
		return nil, err
	}
	priv, err := LoadPrivateKey(cfg.DataDir)
	if err != nil {
		return nil, errors.Wrapf(err, "LoadPrivateKey err")
	}

	ipLimiter := leakybucket.NewCollector(ipLimit, ipBurst, 30*time.Second, true /* deleteEmptyBuckets */)

	s := &Server{
		ctx:          ctx,
		cancel:       cancel,
		cfg:          cfg,
		ipLimiter:    ipLimiter,
		privKey:      priv,
		joinedTopics: make(map[string]*pubsub.Topic, 8),
	}

	// setup Kad-DHT discovery
	dopts := []kaddht.Option{
		kaddht.Mode(kaddht.ModeServer),
		kaddht.ProtocolPrefix("/bsc/validator/disc"),
	}
	routingCfg := func(h host.Host) (routing.PeerRouting, error) {
		var err error
		s.dht, err = kaddht.New(ctx, h, dopts...)
		return s.dht, err
	}

	// setup libp2p instance
	opts, err := s.buildOptions()
	if err != nil {
		return nil, errors.Wrapf(err, "buildOptions err")
	}
	opts = append(opts, libp2p.Routing(routingCfg))
	gomplex.ResetStreamTimeout = 5 * time.Second

	h, err := libp2p.New(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "create p2p host err")
	}
	s.host = h

	// setup Gossipsub immediately
	psOpts := s.pubsubOptions()
	// We have to unfortunately set this globally in order
	// to configure our message id time-cache rather than instantiating
	// it with a router instance.
	pubsub.TimeCacheDuration = 2 * oneEpochDuration()

	gs, err := pubsub.NewGossipSub(s.ctx, s.host, psOpts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create pubsub")
	}
	s.pubsub = gs

	// setup peer watcher
	watcher, err := NewPeerWatcher(&WatcherConfig{
		PeerLimit:            cfg.MaxPeers,
		BadRespThreshold:     defaultBadRespThreshold,
		BadRespDecayInterval: defaultBadRespDecayInterval,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create peer watcher")
	}
	s.watcher = watcher
	return s, nil
}

func (s *Server) Start() {
	if s.started {
		log.Error("libp2p already started, skip...")
		return
	}

	// setup discovery
	bootPeers := s.connectPeersFromAddr(s.cfg.BootStrapAddrs)
	for _, p := range bootPeers {
		s.host.ConnManager().Protect(p.ID, "bootnode")
	}
	if err := s.dht.Bootstrap(s.ctx); err != nil {
		log.Error("Kad-DHT bootstrap failed", "err", err)
	}
	s.started = true

	staticPeers := s.connectPeersFromAddr(s.cfg.StaticPeers)
	for _, p := range staticPeers {
		// TODO(galaio): set trust peer
		//s.watcher.SetTrustedPeers(p.ID)
		_ = p
	}

	// Periodic functions.
	// TODO(galaio): add more Periodic logic
	// 1. retry connect static peers
	// 2. prune peer in every 30 minutes
	// 3. metrics updates, report connect peers, inbound, outbound, tcp, quic

	listenAddrs := s.host.Network().ListenAddresses()
	log.Info("libp2p started at:", "addrs", listenAddrs)
}

func (s *Server) connectPeersFromAddr(addrs []string) []peer.AddrInfo {
	parsedAddrs, err := ParsePeersAddr(addrs)
	if err != nil {
		log.Error("fail to parse boot strap addr")
		return nil
	}

	var successed []peer.AddrInfo
	for _, addr := range parsedAddrs {
		err := s.host.Connect(context.Background(), addr)
		if err != nil {
			log.Warn("cannot connect the boot node", "addr", addr)
		}
		successed = append(successed, addr)
	}
	return successed
}

func (s *Server) Stop() {
	defer s.cancel()
	s.started = false
	if s.dht != nil {
		s.dht.Close()
	}
}

func (s *Server) buildOptions() ([]libp2p.Option, error) {
	ipAddr := net.ParseIP(s.cfg.HostAddress)
	tcpPort := s.cfg.TCPPort
	quicPort := s.cfg.QUICPort
	var ipType string
	if ipAddr.To4() != nil {
		ipType = "ip4"
	} else if ipAddr.To16() != nil {
		ipType = "ip6"
	} else {
		return nil, errors.New("unsupported ip address")
	}

	// Example: /ip4/1.2.3.4./tcp/5678
	multiAddrTCP, err := multiaddr.NewMultiaddr(fmt.Sprintf("/%s/%s/tcp/%d", ipType, ipAddr, tcpPort))
	if err != nil {
		return nil, errors.Wrapf(err, "QUIC NewMultiaddr fail from %s:%d", ipAddr, tcpPort)
	}
	multiaddrs := []multiaddr.Multiaddr{multiAddrTCP}
	if s.cfg.EnableQuic {
		// Example: /ip4/1.2.3.4/udp/5678/quic-v1
		multiAddrQUIC, err := multiaddr.NewMultiaddr(fmt.Sprintf("/%s/%s/udp/%d/quic-v1", ipType, ipAddr, quicPort))
		if err != nil {
			return nil, errors.Wrapf(err, "QUIC NewMultiaddr fail from %s:%d", ipAddr, tcpPort)
		}

		multiaddrs = append(multiaddrs, multiAddrQUIC)
	}

	ipriv, err := ConvertToInterfacePrivkey(s.privKey)
	if err != nil {
		return nil, errors.Wrapf(err, "ConvertToInterfacePrivkey fail")
	}
	id, err := peer.IDFromPrivateKey(ipriv)
	if err != nil {
		return nil, errors.Wrapf(err, "IDFromPrivateKey fail, key: %s", ipriv.GetPublic().Type().String())
	}

	log.Info("setup libp2p with peerID: %s ", id.String())
	options := []libp2p.Option{
		privKeyOption(s.privKey),
		libp2p.ListenAddrs(multiaddrs...),
		libp2p.UserAgent("valp2p-POC Alpha"),
		libp2p.ConnectionGater(s),
		libp2p.Transport(libp2ptcp.NewTCPTransport),
		libp2p.DefaultMuxers,
		libp2p.Muxer("/mplex/6.7.0", mplex.DefaultTransport),
		libp2p.Security(noise.ID, noise.New),
		//libp2p.Ping(false),    // Disable Ping Service.
		libp2p.DisableRelay(), // Disable relay transport, just connect directly
	}

	if s.cfg.EnableQuic {
		options = append(options, libp2p.Transport(libp2pquic.NewTransport))
	}

	// TODO(galaio): confirm ResourceManager
	//if disableResourceManager {
	//	options = append(options, libp2p.ResourceManager(&network.NullResourceManager{}))
	//}

	return options, nil
}

// Started returns true if the p2p service has successfully started.
func (s *Server) Started() bool {
	return s.started
}

// PubSub returns the p2p pubsub framework.
func (s *Server) PubSub() *pubsub.PubSub {
	return s.pubsub
}

// Host returns the currently running libp2p
// host of the service.
func (s *Server) Host() host.Host {
	return s.host
}

// PeerID returns the Peer ID of the local peer.
func (s *Server) PeerID() peer.ID {
	return s.host.ID()
}

// Disconnect from a peer.
func (s *Server) Disconnect(pid peer.ID) error {
	return s.host.Network().ClosePeer(pid)
}

// Connect to a specific peer.
func (s *Server) Connect(pi peer.AddrInfo) error {
	return s.host.Connect(s.ctx, pi)
}

func privKeyOption(privkey *ecdsa.PrivateKey) libp2p.Option {
	return func(cfg *libp2p.Config) error {
		ifaceKey, err := ConvertToInterfacePrivkey(privkey)
		if err != nil {
			return err
		}
		return cfg.Apply(libp2p.Identity(ifaceKey))
	}
}
