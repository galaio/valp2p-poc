package main

import (
	"context"
	"flag"
	"fmt"
	golog "github.com/ipfs/go-log/v2"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/galaio/valp2p-poc/p2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/exp/rand"
)

var (
	port                = flag.Int("port", 13000, "Port to listen on")
	bootstrap           = flag.String("bootstrap", "/ip4/127.0.0.1/tcp/63785/p2p/QmWjz6xb8v9K4KnYEwP5Yk75k5mMBCehzWFLCvvQpYxF3d", "Comma separated list of bootstrap peers")
	localPeerID peer.ID = ""
	cachedPeers         = make(map[peer.ID]struct{})
)

func main() {
	flag.Parse()
	// LibP2P code uses golog to log messages. They log with different
	// string IDs (i.e. "swarm"). We can control the verbosity level for
	// all loggers with:
	//golog.SetAllLoggers(golog.LevelDebug) // Change to INFO for extra info
	golog.SetAllLoggers(golog.LevelError)

	cfg := &p2p.Config{
		StaticPeers: nil,
		HostAddress: "127.0.0.1",
		DataDir:     "./p2p",
		//QUICPort:       13000,
		//TCPPort:        13000,
		QUICPort:     uint(*port),
		TCPPort:      uint(*port),
		PingInterval: 1,
		MaxPeers:     100,
		EnableQuic:   true,
	}
	if len(*bootstrap) > 0 {
		cfg.BootStrapAddrs = []string{*bootstrap}
	}
	server, err := p2p.NewServer(cfg)
	if err != nil {
		panic(err)
	}

	// start p2p, and discovery
	server.Start()
	defer server.Stop()

	fmt.Printf("p2p started, addrs: %v, peer: %v\n", server.Host().Addrs(), server.PeerID())
	localPeerID = server.PeerID()
	// register topic
	blockSub, err := server.SubscribeToTopic(p2p.BlockTopic)
	if err != nil {
		panic(err)
	}
	go blockHandle(blockSub)
	voteSub, err := server.SubscribeToTopic(p2p.VoteTopic)
	go voteHandle(voteSub)
	go randomBlockAndVote(server)

	// register ping msg
	server.SetStreamHandler(p2p.TopicPrefix+"ping", func(stream network.Stream) {
		defer stream.Close()
		data, err := io.ReadAll(stream)
		if err != nil {
			fmt.Println("read ping resp err", err)
			return
		}
		fmt.Printf("receive %v from %v\n", string(data), stream.Conn().RemotePeer())
		_, err = stream.Write([]byte(fmt.Sprintf("pong_%d", *port)))
		if err != nil {
			fmt.Println("write ping resp err", err)
			return
		}
		cachedPeers[stream.Conn().RemotePeer()] = struct{}{}
	})
	go randomPing(server)
	go func() {
		for {
			t := time.After(10 * time.Second)
			select {
			case <-t:
				if len(cachedPeers) == 0 {
					continue
				}
				var gossipPeers []peer.ID
				for id := range cachedPeers {
					gossipPeers = append(gossipPeers, id)
				}
				cnnPeers := server.Host().Network().Peers()
				fmt.Printf("peer stats:\ngossip peers: %d|%v\nconnected peers: %d|%v\n", len(gossipPeers), gossipPeers, len(cnnPeers), cnnPeers)
			}
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT)
	select {
	case <-stop:
		os.Exit(0)
	}
}

func randomPing(server *p2p.Server) {
	for {
		t := time.After(time.Duration(rand.Int()%10+30) * time.Second)
		select {
		case <-t:
			ctx := context.Background()
			cnnPeers := server.Host().Network().Peers()
			if len(cnnPeers) == 0 {
				continue
			}
			pid := cnnPeers[rand.Int()%len(cnnPeers)]
			stream, err := server.Send(ctx, fmt.Sprintf("ping_%d", *port), p2p.TopicPrefix+"ping", pid)
			if err != nil {
				fmt.Println("Send ping err", err)
				continue
			}
			data, err := io.ReadAll(stream)
			if err != nil {
				fmt.Println("read ping resp err", err)
				continue
			}
			fmt.Printf("receive %v from %v\n", string(data), pid)
			stream.Close()
		}
	}
}

func voteHandle(sub *pubsub.Subscription) {
	ctx := context.Background()
	defer sub.Cancel()
	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			fmt.Println("voteHandle err", err)
			continue
		}
		if msg.GetFrom() == localPeerID {
			continue
		}
		fmt.Printf("receive %v from %v\n", string(msg.Data), msg.GetFrom())
		cachedPeers[msg.GetFrom()] = struct{}{}
	}
}

func blockHandle(sub *pubsub.Subscription) {
	ctx := context.Background()
	defer sub.Cancel()
	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			fmt.Println("blockHandle err", err)
			continue
		}
		from := msg.GetFrom()
		if from == localPeerID {
			continue
		}
		fmt.Printf("receive %v from %v\n", string(msg.Data), from)
		cachedPeers[from] = struct{}{}
	}
}

func randomBlockAndVote(server *p2p.Server) {
	num := 0
	for {
		t := time.After(time.Duration(rand.Int()%10+30) * time.Second)
		select {
		case <-t:
			ctx := context.Background()
			server.PublishToTopic(ctx, p2p.BlockTopic, []byte(fmt.Sprintf("block_%d_%d", *port, num)))
			server.PublishToTopic(ctx, p2p.VoteTopic, []byte(fmt.Sprintf("vote_%d_%d", *port, num)))
			num++
		}
	}
}
