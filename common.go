package main

const (
	TxChannelName            = "tx"
	DataChannelName          = "data"
	MaxDiscordFileSize       = 8388119
	MaxDiscordMessageRequest = 100
)

const (
	WriteTx int = iota
	DeleteTx
)

const (
	FileType int = iota
	FolderType
)

var (
	DataChannelID string
	TxChannelID   string
)
