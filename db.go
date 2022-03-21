package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/bwmarrin/discordgo"
	"github.com/hashicorp/go-immutable-radix"
	"go.uber.org/atomic"
)

var (
	// We need to jankily expose the db for messageCreate
	db      *DB
	txReady = atomic.NewBool(false)
)

type DB struct {
	radix *iradix.Tree
}

func setupDB(dg *discordgo.Session, guildID string) (*DB, error) {
	defer txReady.Store(true)

	channels, err := dg.GuildChannels(guildID)
	if err != nil {
		return nil, err
	}

	// Find existing channels
	channelMap := map[string]*discordgo.Channel{
		DataChannelName: nil,
		TxChannelName:   nil,
	}
	for _, channel := range channels {
		if _, ok := channelMap[channel.Name]; ok {
			channelMap[channel.Name] = channel
		}
	}

	// Create missing channels
	for name, channel := range channelMap {
		if channel == nil {
			create, err := dg.GuildChannelCreate(guildID, name, discordgo.ChannelTypeGuildText)
			if err != nil {
				return nil, err
			}
			channelMap[name] = create
		}
	}

	DataChannelID = channelMap[DataChannelName].ID
	TxChannelID = channelMap[TxChannelName].ID

	db := DB{
		radix: iradix.New(),
	}
	channel := channelMap[TxChannelName]

	// We check for the lastPinTimestamp to see which TX to start from
	// this is not necessary, but if we need to speed up DB setup we can
	// compress multiple TXs into one message and re-pin to the new starting
	// TX.
	//
	// If lastPinTimestamp is not found, we insert the root folder to
	// initialize.
	if channel.LastPinTimestamp != nil {
		pinned, err := dg.ChannelMessagesPinned(channel.ID)
		if err != nil {
			return nil, err
		}
		if len(pinned) == 0 {
			return nil, errors.New("pin timestamp found but no pins were found, very weird")
		}

		messages := []*discordgo.Message{pinned[0]}
		for true {
			batch, err := dg.ChannelMessages(
				channel.ID,
				MaxDiscordMessageRequest,
				"", messages[len(messages)-1].ID, "",
			)
			if err != nil {
				return nil, err
			}

			// Messages are in reverse order
			for i, j := 0, len(batch)-1; i < j; i, j = i+1, j-1 {
				batch[i], batch[j] = batch[j], batch[i]
			}

			// Apply TX batch
			applyMessageTxs(&db, batch)
			if len(batch) != MaxDiscordMessageRequest {
				break
			}
		}
	} else {
		tx := Tx{
			Tx:   WriteTx,
			Path: "/",
			Type: FolderType,
		}
		db.radix, _, _ = db.radix.Insert([]byte("/"), tx)
		b, _ := json.Marshal(tx)
		msg, err := dg.ChannelFileSend(
			channel.ID,
			TxChannelName,
			bytes.NewReader(b),
		)
		if err != nil {
			return nil, err
		}
		err = dg.ChannelMessagePin(channel.ID, msg.ID)
		if err != nil {
			return nil, err
		}
	}

	return &db, nil
}
