package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/bwmarrin/discordgo"
	"github.com/hashicorp/go-immutable-radix"
)

type DB struct {
	radix         *iradix.Tree
	dataChannelID string
	txChannelID   string
}

func setupDB(dg *discordgo.Session, guildID string) (*DB, error) {
	channels, err := dg.GuildChannels(guildID)
	if err != nil {
		return nil, err
	}

	channelMap := map[string]*discordgo.Channel{
		DataChannelName: nil,
		TxChannelName:   nil,
	}
	for _, channel := range channels {
		if _, ok := channelMap[channel.Name]; ok {
			channelMap[channel.Name] = channel
		}
	}

	for name, channel := range channelMap {
		if channel == nil {
			create, err := dg.GuildChannelCreate(guildID, name, discordgo.ChannelTypeGuildText)
			if err != nil {
				return nil, err
			}
			channelMap[name] = create
		}
	}

	channel := channelMap[TxChannelName]
	db := DB{
		radix:         iradix.New(),
		dataChannelID: channelMap[DataChannelName].ID,
		txChannelID:   channelMap[TxChannelName].ID,
	}
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

			// Reverse
			for i, j := 0, len(batch)-1; i < j; i, j = i+1, j-1 {
				batch[i], batch[j] = batch[j], batch[i]
			}

			messages = append(messages, batch...)
			if len(batch) != MaxDiscordMessageRequest {
				break
			}
		}

		applyMessageTxs(&db, messages)
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
