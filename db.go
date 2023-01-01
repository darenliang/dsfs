package main

import (
	"bytes"
	"errors"
	"github.com/bwmarrin/discordgo"
	"github.com/hashicorp/go-immutable-radix/v2"
	"go.uber.org/atomic"
	"io"
)

var (
	dbReady = atomic.NewBool(false)
)

type DB struct {
	radix *iradix.Tree[Tx]
}

// setupDB setups the in-mem database
// This function needs to be refactored; it looks really gross in its current
// state.
func setupDB(dg *discordgo.Session, guildID string) (*DB, error) {
	defer dbReady.Store(true)

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

	db := &DB{
		radix: iradix.New[Tx](),
	}
	channel := channelMap[TxChannelName]

	txBuffer := &bytes.Buffer{}
	var pinnedMsg *discordgo.Message

	// We check for the lastPinTimestamp to see which TX to start from
	// this is not necessary, but if we need to speed up DB setup we can
	// compress multiple TXs into one message and re-pin to the new starting
	// TX.
	//
	// If lastPinTimestamp is not found, we insert the root folder to
	// initialize.
	if channel.LastPinTimestamp != nil {
		pinnedMsgs, err := dg.ChannelMessagesPinned(channel.ID)
		if err != nil {
			return nil, err
		}
		if len(pinnedMsgs) == 0 {
			return nil, errors.New("pin timestamp found but no pins were found, very weird")
		}

		// Get the latest pinned message
		pinnedMsg = pinnedMsgs[len(pinnedMsgs)-1]
		messages := []*discordgo.Message{pinnedMsg}
		for {
			batch, err := dg.ChannelMessages(
				channel.ID,
				MaxDiscordMessageRequest,
				"", messages[len(messages)-1].ID, "",
			)
			if err != nil {
				return nil, err
			}
			if len(batch) == 0 {
				break
			}

			// Messages are in reverse order
			for i, j := 0, len(batch)-1; i < j; i, j = i+1, j-1 {
				batch[i], batch[j] = batch[j], batch[i]
			}
			messages = append(messages, batch...)

			if len(batch) != MaxDiscordMessageRequest {
				break
			}
		}

		if compact {
			logger.Info("compacting TXs")
			applyMessageTxs(db, messages, txBuffer, false)
		} else {
			applyMessageTxs(db, messages, nil, false)
		}
	} else {
		tx := Tx{
			Tx:   WriteTx,
			Path: "/",
			Type: FolderType,
		}
		db.radix, _, _ = db.radix.Insert([]byte("/"), tx)
		b, _ := json.Marshal(tx)
		pinnedMsg, err = dg.ChannelFileSend(
			channel.ID,
			TxChannelName,
			bytes.NewReader(b),
		)
		if err != nil {
			return nil, err
		}
		err = dg.ChannelMessagePin(channel.ID, pinnedMsg.ID)
		if err != nil {
			return nil, err
		}
	}

	// Return early if compaction is not needed
	if !compact {
		logger.Info("compaction not needed")
		return db, nil
	}

	messageBuffer := make([]byte, 0, MaxDiscordFileSize)
	var firstMsg *discordgo.Message
	for {
		b, err := txBuffer.ReadBytes('\n')
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			logger.Warn("failed to read message buffer", err)
			return db, nil
		}
		if err != nil || len(b) == 0 {
			break
		}
		if len(messageBuffer)+len(b) > MaxDiscordFileSize {
			msg, err := dg.ChannelFileSend(TxChannelID, TxChannelName, bytes.NewReader(messageBuffer))
			if err != nil {
				logger.Warn("aborting transaction compaction", err)
				return db, nil
			}
			if firstMsg == nil {
				firstMsg = msg
			}
			// Keep underlying allocated memory
			messageBuffer = messageBuffer[:0]
		} else {
			messageBuffer = append(messageBuffer, b...)
		}
	}

	// Check if messageBuffer has outstanding transactions
	if len(messageBuffer) != 0 {
		msg, err := dg.ChannelFileSend(TxChannelID, TxChannelName, bytes.NewReader(messageBuffer))
		if err != nil {
			logger.Warn("aborting transaction compaction", err)
			return db, nil
		}
		if firstMsg == nil {
			firstMsg = msg
		}
	}

	if firstMsg != nil {
		err := dg.ChannelMessagePin(TxChannelID, firstMsg.ID)
		if err != nil {
			logger.Warn("failed to pin new transaction start point")
			return db, nil
		}
		err = dg.ChannelMessageUnpin(TxChannelID, pinnedMsg.ID)
		if err != nil {
			logger.Warn("failed to unpin old transaction start point, please manually unpin old pinned messages")
			return db, nil
		}
	}

	return db, nil
}
