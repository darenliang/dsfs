package main

const (
	TxChannelName            = "tx"
	DataChannelName          = "data"
	MaxDiscordFileSize       = 8388119
	MaxDiscordMessageRequest = 100
	PollInterval             = 250
	MaxRetries               = 20
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
	UniqueID      string
	DataChannelID string
	TxChannelID   string
)
