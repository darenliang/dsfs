package main

import "time"

const (
	TxChannelName            = "tx"
	DataChannelName          = "data"
	MaxDiscordFileSize       = 8388119
	MaxDiscordMessageRequest = 100
	MaxDiscordFileCount      = 10
	PollInterval             = 250 * time.Millisecond
	MaxRetries               = 20
	QueueTimeout             = 5 * time.Second
)

const (
	WriteTx int = iota
	DeleteTx
)

const (
	FileType int = iota
	FolderType
)
