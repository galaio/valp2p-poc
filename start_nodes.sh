#!/bin/bash

# 定义函数，用于启动单个节点
launch_node() {
    local port=$1
    local bootnode=$2
    local log_file=$3

    # 构造启动命令
    nohup ./val_p2p  --port "$port" --bootstrap "$bootnode" > "$log_file" 2>&1 &
    echo "Node started on port $port with bootnode $bootnode. Logging to $log_file"
}

# 主函数，解析参数并启动多个节点
main() {
    # 解析命令行参数
    NUM_NODES=${1:-5}          # 节点数量，默认为5
    START_PORT=${2:-13001}      # 起始端口，默认为13001
    BOOTNODE=${3:-""}          # 启动节点，默认为空

    # 启动多个节点
    for ((i=0; i<NUM_NODES; i++)); do
        port=$((START_PORT + i))
        log_file="./logs/node_${port}.log"
        launch_node "$port" "$BOOTNODE" "$log_file"
        sleep 10
    done

    echo "All nodes started. Logs are in node_<port>.log files."
}

# 调用主函数
main "$@"