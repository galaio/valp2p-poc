# valp2p-poc

## Usage

```bash
# clean & build
pkill val_p2p
rm -rf logs/*.log && go build -o val_p2p main.go

# start bootnode first
nohup ./val_p2p > ./logs/node_13000.log 2>&1 &

# start other nodes that connect to bootnode
./val_p2p --port 13001 --bootstrap "/ip4/127.0.0.1/tcp/13000/p2p/16Uiu2HAmFeHGFJFBnv8zrBeMDqRwfXHK2TgAZLoyprXfgkrU6BuJ"

# batch startup
bash start_nodes.sh 21 13001 "/ip4/127.0.0.1/tcp/13000/p2p/16Uiu2HAmFeHGFJFBnv8zrBeMDqRwfXHK2TgAZLoyprXfgkrU6BuJ"
```